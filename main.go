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

func approvePr(org string, repo string, prNum int) {

	event := "APPROVE"

	review := &github.PullRequestReviewRequest{Event: &event}

	_, _, err := client.PullRequests.CreateReview(ctx, org, repo, prNum, review)

	if err != nil {
		panic(err)
	}
	fmt.Println("Approved")
}

func waitForComment(ctx context.Context, client github.Client, org string, repo string, prNum int, match string) (*github.IssueComment, error) {
	opt_cmt := &github.IssueListCommentsOptions{}

	var comments []*github.IssueComment
	var err error
	max_tries := 20
	i := 0

	fmt.Printf("Retrieving comments ... ")

	f := func() error {

		comments, _, err = client.Issues.ListComments(ctx, org, repo, prNum, opt_cmt)
		if err != nil {
			fmt.Println(err)
			return backoff.Permanent(err)
		}

		if len(comments) > 0 {
			bodyContent := comments[len(comments)-1].GetBody()
			if strings.Contains(bodyContent, match) {
				fmt.Println(" Result found")
				return nil
			}
		}

		if max_tries < i {
			if len(comments) > 0 {
				bodyContent := comments[len(comments)-1].GetBody()
				fmt.Println("Expected comment not found, latest comment:\n")
				fmt.Println(bodyContent, "\n")
			}
			return backoff.Permanent(errors.New("Timeout. Expected comment not found"))
		}

		i += 1

		fmt.Printf(".")
		return errors.New("error")
	}

	err = backoff.RetryNotifyWithTimer(f, backoff.NewExponentialBackOff(), nil, nil)
	return comments[len(comments)-1], err
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

	// wait for a comment with the output from Atlantis Plan
	comment, err := waitForComment(ctx, *client, org, repo, prNum, "Ran Plan for dir")

	if err != nil {
		errorStr := fmt.Sprintf("Error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	bodyContent := comment.GetBody()

	// fail if the result of the plan is not successful
	if strings.Contains(bodyContent, "Plan Error") {
		errorStr := fmt.Sprintf("Plan failed")
		fmt.Println(errorStr, ", please review:\n")
		fmt.Println(bodyContent, "\n")
		panic(errors.New(errorStr))
	}
	// if plan was successful, return the line containing the terragrunt directory
	firstLine := strings.Split(bodyContent, "\n")[0]
	fmt.Println(firstLine)
	return firstLine
}

func waitApply(org string, repo string, prNum int) {

	// wait for a comment with the output from Atlantis Plan
	comment, err := waitForComment(ctx, *client, org, repo, prNum, "Ran Apply for dir")

	if err != nil {
		errorStr := fmt.Sprintf("Error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	bodyContent := comment.GetBody()

	// fail if the result of the plan is not successful
	if strings.Contains(bodyContent, "Apply Error") {
		errorStr := fmt.Sprintf("Apply failed")
		fmt.Println(errorStr, ", please review:\n")
		fmt.Println(bodyContent, "\n")
		panic(errors.New(errorStr))
	}
	// apply was successful
	firstLine := strings.Split(bodyContent, "\n")[0]
	fmt.Println(firstLine)
	fmt.Println("PR is OK to Merge!")
}

func runApply(org string, repo string, prNum int, atlantisPath string) {

	workspace := strings.ReplaceAll(atlantisPath, "/", "_")

	comment := fmt.Sprintf("atlantis apply -d %s -w %s", atlantisPath, workspace)

	postComment(ctx, *client, comment, org, repo, prNum)
	fmt.Println(fmt.Sprintf("Commented `%s`", comment))
	fmt.Println("Waiting for apply to start...")
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
	lastComment := waitPlan(org, repo, pr)
	atlantisPath = strings.Split(lastComment, "`")[1]
	// Approve the PR
	approvePr(org, repo, pr)
	// Apply changes
	runApply(org, repo, pr, atlantisPath)
	// Wait for atlantis apply result to appear in PR
	waitApply(org, repo, pr)
}
