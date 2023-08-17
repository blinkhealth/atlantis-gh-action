package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-github/v53/github"
	"golang.org/x/oauth2"
)

const (
	timeOut             = 2 * time.Second
	initialInterval     = 800 * time.Millisecond
	randomizationFactor = 1.5
	multiplier          = 3
	maxInterval         = 15 * time.Second
	maxElapsedTime      = 20 * time.Minute
	BlinkGitHubUser     = "blinkhealthgithub"

	/* Time elapsed between pull request 'create time' and the time it took
	 * Atlantis to create the comment - which is the terragrunt plan.  Multiple
	 * comments from Atlantis __can__ exist, but we only care about the most
	 * recent one.
	 */
	AcceptableTimeDelta = 65 //seconds
)

var client *github.Client
var ctx context.Context = context.Background()
var atlantisPath string

func getPrCreatedAt(ctx context.Context, client github.Client, org string, repo string, prNum int) github.Timestamp {
    pr, _, err := client.PullRequests.Get(ctx, org, repo, prNum)
	if err != nil {
		panic(err)
	}
	return *pr.CreatedAt
}

func prIsMerged(ctx context.Context, client github.Client, org string, repo string, prNum int) bool {
    pr, _, err := client.PullRequests.Get(ctx, org, repo, prNum)

	if err != nil {
		panic(err)
	}
	return *pr.Merged
}

func approvePr(org string, repo string, prNum int) {

	event := "APPROVE"

	review := &github.PullRequestReviewRequest{Event: &event}

	_, _, err := client.PullRequests.CreateReview(ctx, org, repo, prNum, review)

	if err != nil {
		panic(err)
	}
	fmt.Println("Approved")
}

func waitForComment(ctx context.Context, client github.Client, org string, repo string, prNum int, match string, errorMatch string) (*github.IssueComment, error) {

	/* EMPTY options - because it don't work!
     * Instead, iterate over the comments (below) in reverse order because
     * github API sux  or I can't seem to figure out how to sort by date. FWIW,
     * 'curl' call  returns the same order as this code does - and implies their
     * API does not honor query parameters - giving up and explore comments
     * backwards
     */
	opt := &github.IssueListCommentsOptions{}

	fmt.Println("Retrieving comments...")

	// callback to pass to the retry
	commentSearch := func() (*github.IssueComment, error) {
		prc := getPrCreatedAt(ctx, client, org, repo, prNum)
		comments, _, err := client.Issues.ListComments(ctx, org, repo, prNum, opt)
		if err != nil {
			fmt.Println(err)
			return nil, backoff.Permanent(err)
		}

		for idx := len(comments) - 1; idx >= 0; idx-- {
			c := comments[idx]
			u := c.GetUser()
            fmt.Printf("%s\n", *u.Login)
            continue
			if *u.Login == BlinkGitHubUser {
				bodyContent := c.GetBody()

				// quickly fail on error
				if strings.Contains(bodyContent, errorMatch) {
					fmt.Println(" Error found, latest comment:\n")
					fmt.Println(bodyContent, "\n")
					return nil, backoff.Permanent(errors.New("Error found"))
				}

				/* Brute force ignore anything that takes longer.  If we fall
				 * into this clause - figure out why it takes so long for
				 * Atlantis to emit/apply a plan or increase the delta.
                 * Increasing the should probably be the last resort.
				 */
				planCreated := c.GetCreatedAt()
				td := int(prc.Sub(*planCreated.GetTime()).Abs().Seconds())
				if strings.Contains(bodyContent, match) && td <= AcceptableTimeDelta {
					fmt.Printf("Result found: user: %s comment created at:[%s] pr created at:[%s] time delta: %d\n", *u.Login, c.GetCreatedAt(), prc, td)
					return c, nil
				} else if strings.Contains(bodyContent, match) && td > AcceptableTimeDelta {
					errMsg := fmt.Sprintf("Took longer than %d sec to emit a plan. PR created @ %s; plan created @ %s", td, prc, planCreated)
					return nil, errors.New(errMsg)
				}
			}
			// otherwise skip the PR message
			fmt.Printf("Skipping: user: %s comment created at:[%s] pr created at:[%s]\n", *u.Login, c.GetCreatedAt(), prc)
		}
		errMsg := fmt.Sprintf("Unexpected error - reached Timeout of ~ %.1f minutes.", maxElapsedTime.Minutes())
		return nil, errors.New(errMsg)
	}

	exp := backoff.NewExponentialBackOff()
	exp.InitialInterval = initialInterval
	exp.RandomizationFactor = randomizationFactor
	exp.Multiplier = multiplier
	exp.MaxInterval = maxInterval
	exp.MaxElapsedTime = maxElapsedTime

	comment, err := backoff.RetryNotifyWithTimerAndData(commentSearch, exp, nil, nil)

	if err != nil {
		return nil, err
	}
	return comment, nil
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

	// Wait for a comment with the output from Atlantis Plan
	// Fail if Atlantis returns an error

	var bodyContent string
	var firstLine string

	comment, err := waitForComment(ctx, *client, org, repo, prNum, "Ran Plan for dir", "Plan Error")

	if err != nil {
		errorStr := fmt.Sprintf("Error: %s", err.Error())
		fmt.Printf("why this error: %v\n", errorStr)
		// known Atlantis issue, sometimes autoplan fails to retrieve PR data, we just need to run `atlantis plan` again
		if strings.Contains(bodyContent, "404 Not Found") {
			postComment(ctx, *client, "atlantis plan", org, repo, prNum)
			return waitPlan(org, repo, prNum)
		} else {
			// for other errors, abort execution
			panic(errors.New(errorStr))
		}
	}

	// if plan was successful, return the line containing the terragrunt directory
	bodyContent = comment.GetBody()
	firstLine = strings.Split(bodyContent, "\n")[0]
	fmt.Printf("returning first line : %s\n", firstLine)
	return firstLine
}

func waitApply(org string, repo string, prNum int) {

	// Wait for a comment with the output from Atlantis Apply
	// Fail if Atlantis returns an error
	comment, err := waitForComment(ctx, *client, org, repo, prNum, "Ran Apply for dir", "Apply Error")

	if err != nil {
		errorStr := fmt.Sprintf("Error: %s", err.Error())
		panic(errors.New(errorStr))
	}

	// Apply was successful, show output and move on
	bodyContent := comment.GetBody()
	fmt.Println(bodyContent, "\n")
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

	token := os.Getenv("GITHUB_API_TOKEN")
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	tc := oauth2.NewClient(ctx, ts)
	client = github.NewClient(tc)

	repo := os.Getenv("GITHUB_REPOSITORY")
	org, repo := splitRepo(repo)
	pr, _ := strconv.Atoi(os.Args[1])
	fmt.Println(fmt.Sprintf("PROCESSING PR %s/%s/pull/%s", org, repo, strconv.Itoa(pr)))

	if prIsMerged(ctx, *client, org, repo, pr) {
		fmt.Println("This PR has already been Merged, skipping.")
	} else {
		foundComment := waitPlan(org, repo, pr)
		fmt.Printf("comment size: %v\n", len(foundComment))
		fmt.Printf("comment: %v\n", foundComment[len(foundComment)-1])
		atlantisPath = strings.Split(foundComment, "`")[1]
		approvePr(org, repo, pr)
		time.Sleep(timeOut)
		runApply(org, repo, pr, atlantisPath)
		waitApply(org, repo, pr)
	}
}
