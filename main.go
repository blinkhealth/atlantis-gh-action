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

var client *github.Client
var ctx context.Context = context.Background()
var atlantisPath string

func approvePr(repo string, prNum int, org string) {

	event := "APPROVE"

	review := &github.PullRequestReviewRequest{Event: &event}

	_, _, err := client.PullRequests.CreateReview(ctx, org, repo, prNum, review)

	if err != nil {
		panic(err)
	}
	fmt.Println("Approved")
}

func getComments(ctx context.Context, client github.Client, threshold int, org string, repo string, prNum int) ([]*github.IssueComment, error) {
	opt_cmt := &github.IssueListCommentsOptions{}

	var comments []*github.IssueComment
	var err error
	tries := 20
	i := 0

	f := func() error {

		fmt.Printf("Retrieving comments....")

		comments, _, err = client.Issues.ListComments(ctx, org, repo, prNum, opt_cmt)
		if err != nil {
			fmt.Println(err)
			return backoff.Permanent(err)
		}

		fmt.Println("current number of comments:", len(comments))

		if len(comments) > threshold {
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

func waitPlan(org string, repo string, prNum int) string {

	comments, err := getComments(ctx, *client, 0, org, repo, prNum)

	if err != nil {
		errorStr := fmt.Sprintf("unexpected error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	bodyContent := comments[len(comments)-1].GetBody()

	if !strings.Contains(bodyContent, "Ran Plan for dir") {
		errorStr := fmt.Sprintf("Unexpected comment found")
		fmt.Println(errorStr, ", please review:\n")
		fmt.Println(bodyContent, "\n")
		panic(errors.New(errorStr))
	}
	firstLine := strings.Split(bodyContent, "\n")[0]
	fmt.Println(firstLine)
	return firstLine
}

func runApply(org string, repo string, prNum int, atlantisPath string) {

	workspace := strings.ReplaceAll(atlantisPath, "/", "_")

	comment := fmt.Sprintf("atlantis apply -d %s -w %s", atlantisPath, workspace)

	postComment(ctx, *client, comment, org, repo, prNum)
	fmt.Println(fmt.Sprintf("Commented `%s`", comment))
}

func main() {

	// Initialize a GH API client
	token := os.Getenv("GITHUB_API_TOKEN")
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client = github.NewClient(tc)

	// Retrieve repo name and org from GHA context
	repo := os.Getenv("GITHUB_REPOSITORY")
	org, repo := splitRepo(repo)
	// Read the PR number from command line
	pr, _ := strconv.Atoi(os.Args[1])

	fmt.Println(fmt.Sprintf("PROCESSING PR %s/%s/pull/%s", org, repo, strconv.Itoa(pr)))

	// Wait for atlantis plan result to appear in PR
	// 	TODO - Validate plan was successful
	lastComment := waitPlan(org, repo, pr)
	atlantisPath = strings.Split(lastComment, "`")[1]
	// Approve the PR
	approvePr(repo, pr, org)
	// Apply changes
	runApply(org, repo, pr, atlantisPath)
}
