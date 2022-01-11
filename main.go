package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-github/v39/github"
	"golang.org/x/oauth2"
)

func approvePr(repo string, prNum int, org string) {
	ctx := context.Background()

	token := os.Getenv("TF_VAR_gh_approve_token")

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	event := "APPROVE"

	review := &github.PullRequestReviewRequest{Event: &event}

	_, _, err := client.PullRequests.CreateReview(ctx, org, repo, prNum, review)

	if err != nil {
		panic(err)
	}
}

func getComments(ctx context.Context, client github.Client, threshold int, org string, repo string, prNum int) ([]*github.IssueComment, error) {
	opt_cmt := &github.IssueListCommentsOptions{}

	var comments []*github.IssueComment
	var err error
	tries := 20
	i := 0

	f := func() error {
		comments, _, err = client.Issues.ListComments(ctx, org, repo, prNum, opt_cmt)
		if err != nil {
			fmt.Println(err)
			return backoff.Permanent(err)
		}

		fmt.Printf("Current number of commets %v the target is %v\n", len(comments), threshold)

		if len(comments) > threshold {
			fmt.Println("OK")
			return nil
		}

		if tries < i {
			return backoff.Permanent(errors.New("too may tries"))
		}

		i += 1

		fmt.Printf("Trying to get comments currently on try %v of %v.\n", i, tries)
		return errors.New("error")
	}

	err = backoff.RetryNotifyWithTimer(f, backoff.NewExponentialBackOff(), nil, nil)
	return comments, nil
}

func postComment(ctx context.Context, client github.Client, msg string, org string, repo string, prNum int) {
	comment := &github.IssueComment{Body: &msg}
	_, _, err := client.Issues.CreateComment(ctx, org, repo, prNum, comment)
	if err != nil {
		errorStr := fmt.Sprintf("Error with apply on repo %s and pr %v", repo, prNum)
		panic(errors.New(errorStr))
	}
}

func splitRepo(repo string) (string, string) {
	split := strings.Split(repo, "/")
	return split[0], split[1]
}

func runPlan(org string, repo string, prNum int, atlantisPath string) {
	ctx := context.Background()

	token := os.Getenv("TF_VAR_gh_api_token")

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	comments, err := getComments(ctx, *client, 0, org, repo, prNum)

	if err != nil {
		errorStr := fmt.Sprintf("unexpected error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	bodyContent := comments[0].GetBody()

	if strings.Contains(bodyContent, "Your atlantis.yaml config is up to date!") {
		postComment(ctx, *client, "atlantis plan", org, repo, prNum)
	} else {

		errorStr := fmt.Sprintf("Error with altantis config step %s and pr %v", repo, prNum)
		panic(errors.New(errorStr))
	}

	userNames := []string{"blinkhealth-welcome-to"}

	reviewer := github.ReviewersRequest{Reviewers: userNames}

	_, _, err = client.PullRequests.RequestReviewers(ctx, org, repo, prNum, reviewer)

	if err != nil {
		errorStr := fmt.Sprintf("unexpected error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	comments, err = getComments(ctx, *client, 2, org, repo, prNum)

	if err != nil {
		panic(errors.New("error with getting comments"))
	}

	bodyContent = comments[len(comments)-1].GetBody()

	if !strings.Contains(bodyContent, "Ran Plan for dir") {
		fmt.Println(bodyContent)
		errorStr := fmt.Sprintf("Error with plan on repo %s and pr %v", repo, prNum)

		panic(errors.New(errorStr))
	}
}

func main() {

	repo := os.Getenv("GITHUB_REPOSITORY")

	// Split the repo name into org and repo
	org, repo := splitRepo(repo)

	// Get the pr number from GITHUB_EVENT_PATH
	eventPath := os.Getenv("GITHUB_REF")
	eventPathSplit := strings.Split(eventPath, "/")
	prNum, _ := strconv.Atoi(eventPathSplit[2])

	approvePr("terraform-provider-blinkhealth", prNum, org)

	atlantisPath := os.Getenv("GHA_atlantis_path")

	runPlan(org, repo, prNum, atlantisPath)

}
