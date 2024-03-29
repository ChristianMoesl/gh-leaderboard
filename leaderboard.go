package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cli/go-gh/pkg/auth"
	"github.com/gofri/go-github-ratelimit/github_ratelimit"
	"github.com/jedib0t/go-pretty/progress"
	"github.com/jedib0t/go-pretty/table"

	"github.com/google/go-github/v60/github"
)

var (
	amountOfRequests   atomic.Uint64
	ratelimitRemaining atomic.Uint64
	ratelimitReset     atomic.Pointer[github.Timestamp]
)

func fetchRepositories(client *github.Client, options *Options, clientNumber int, totalClients int, repos chan *github.Repository) {
	page := clientNumber
	for i := 0; true; i++ {
		opts := github.RepositoryListByOrgOptions{
			ListOptions: github.ListOptions{
				Page: page,
			},
		}
		fetchedRepos, response, err := client.Repositories.ListByOrg(context.Background(), options.Organization, &opts)
		if err != nil {
			panic(err)
		}
		for _, fetchedRepo := range fetchedRepos {
			repos <- fetchedRepo
		}

		page = clientNumber + i*totalClients
		if page > response.LastPage {
			return
		}
	}
}

func processPullRequest(client *github.Client, options *Options, repo *github.Repository, pr *github.PullRequest, stats chan *Stats) {
	stats <- &Stats{
		Name:         pr.GetUser().GetLogin(),
		PullRequests: 1,
	}

	slog.Debug("Fetching reviews", "repository", repo.GetName(), "pullRequest", "#"+strconv.Itoa(pr.GetNumber()))
	reviews, response, err := client.PullRequests.ListReviews(context.Background(), repo.GetOwner().GetLogin(), repo.GetName(), pr.GetNumber(), &github.ListOptions{
		PerPage: 100,
	})
	if err != nil {
		panic(err)
	}
	ratelimitRemaining.Store(uint64(response.Rate.Remaining))
	ratelimitReset.Store(&response.Rate.Reset)
	if response.NextPage != 0 {
		log.Fatalf("Found to many reviews in pull request %s/%s#%d to handle\n", repo.GetOwner().GetLogin(), repo.GetName(), pr.GetNumber())
	}

	for _, review := range reviews {
		if review.GetSubmittedAt().After(options.Since) {
			body := review.GetBody()
			lines := len(strings.Split(body, "\n"))
			stats <- &Stats{
				Name:                review.GetUser().GetLogin(),
				Reviews:             1,
				CommentLinesWritten: lines,
			}
		}
	}

	slog.Debug("Fetching comments", "repository", repo.GetName(), "pullRequest", "#"+strconv.Itoa(pr.GetNumber()))
	page := 0
	for {
		comments, response, err := client.PullRequests.ListComments(context.Background(), repo.GetOwner().GetLogin(), repo.GetName(), pr.GetNumber(), &github.PullRequestListCommentsOptions{
			ListOptions: github.ListOptions{
				Page: page,
			},
		})
		if err != nil {
			panic(err)
		}
		ratelimitRemaining.Store(uint64(response.Rate.Remaining))
		ratelimitReset.Store(&response.Rate.Reset)

		for _, comment := range comments {
			if comment.GetCreatedAt().After(options.Since) {
				body := comment.GetBody()
				lines := len(strings.Split(body, "\n"))
				stats <- &Stats{
					Name:                comment.GetUser().GetLogin(),
					Comments:            1,
					CommentLinesWritten: lines,
				}
			}
		}

		if response.NextPage == 0 {
			return
		}
		page = response.NextPage
	}
}

func processRepository(client *github.Client, pw progress.Writer, options *Options, repo *github.Repository, stats chan *Stats) {
	slog.Debug("Fetch pull requests", "repository", repo.GetName())

	page := 0
	var tracker progress.Tracker
	for {
		pullRequests, response, err := client.PullRequests.List(context.Background(), repo.GetOwner().GetLogin(), repo.GetName(), &github.PullRequestListOptions{
			State:     "all",
			Sort:      "updated",
			Direction: "desc",
			ListOptions: github.ListOptions{
				Page: page,
			},
		})
		if err != nil {
			panic(err)
		}
		ratelimitRemaining.Store(uint64(response.Rate.Remaining))
		ratelimitReset.Store(&response.Rate.Reset)

		if page == 0 {
			tracker = progress.Tracker{Message: repo.GetName(), Total: int64(response.LastPage)}
			pw.AppendTracker(&tracker)
		}
		tracker.Increment(1)

		var wg sync.WaitGroup
		for _, pullRequest := range pullRequests {
			if pullRequest.GetUpdatedAt().After(options.Since) {
				wg.Add(1)
				go func(pr *github.PullRequest) {
					defer wg.Done()
					processPullRequest(client, options, repo, pr, stats)
				}(pullRequest)
			}
		}
		wg.Wait()

		if response.NextPage == 0 {
			tracker.Increment(1)
			tracker.MarkAsDone()
			return
		}
		page = response.NextPage
	}
}

type Stats struct {
	Name                string
	PullRequests        int
	Reviews             int
	Comments            int
	CommentLinesWritten int
}

func processRepositories(client *github.Client, pw progress.Writer, options *Options, repos chan *github.Repository, stats chan *Stats) {
	fmt.Printf("Processing data since %v matching repository name pattern %s\n", options.Since, options.NamePattern)

	var wg sync.WaitGroup
	for repo := range repos {
		matched, err := regexp.MatchString(options.NamePattern, repo.GetName())
		if err != nil {
			panic(err)
		}
		if matched {
			wg.Add(1)
			go func(repo *github.Repository) {
				defer wg.Done()
				processRepository(client, pw, options, repo, stats)
			}(repo)
		}
	}
	wg.Wait()
	close(stats)
}

type CountingRoundTripper struct {
	Proxied http.RoundTripper
}

func (crt CountingRoundTripper) RoundTrip(req *http.Request) (res *http.Response, e error) {
	res, e = crt.Proxied.RoundTrip(req)

	amountOfRequests.Add(1)

	return res, e
}

func createClient() *github.Client {
	host, _ := auth.DefaultHost()
	authToken, _ := auth.TokenForHost(host)
	if authToken == "" {
		log.Fatalf("authentication token not found for host %s", host)
	}

	roundTrip := CountingRoundTripper{http.DefaultTransport}
	rateLimiter, err := github_ratelimit.NewRateLimitWaiterClient(roundTrip)
	if err != nil {
		panic(err)
	}
	return github.NewClient(rateLimiter).WithAuthToken(authToken)
}

func accumulateStatsPerUser(stats chan *Stats) map[string]*Stats {
	acc := make(map[string]*Stats)

	for s := range stats {
		userStats := acc[s.Name]
		if userStats == nil {
			userStats = &Stats{
				Name: s.Name,
			}
			acc[userStats.Name] = userStats
		}

		userStats.PullRequests += s.PullRequests
		userStats.Reviews += s.Reviews
		userStats.Comments += s.Comments
		userStats.CommentLinesWritten += s.CommentLinesWritten
	}

	return acc
}

func fetchAllRepositories(client *github.Client, options *Options, repos chan *github.Repository, totalClients int) {
	wg := sync.WaitGroup{}
	for i := 0; i < totalClients; i++ {
		slog.Debug("Spawned repository fetch client", "client", i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fetchRepositories(client, options, i, totalClients, repos)
		}(i)
	}
	wg.Wait()
	close(repos)
}

func initializedLogger() {
	input := os.Getenv("LOG_LEVEL")

	var logLevel slog.Leveler
	switch input {
	case "DEBUG":
		logLevel = slog.LevelDebug
	case "WARN":
		logLevel = slog.LevelWarn
	case "ERROR":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)
}

type Options struct {
	Since        time.Time
	Organization string
	NamePattern  string
}

func parseCliArgs() *Options {
	oneWeek := time.Duration(24*7) * time.Hour
	lastWeek := time.Now().Add(-oneWeek)
	sinceDefault := fmt.Sprintf("%d-%02d-%02d", lastWeek.Year(), lastWeek.Month(), lastWeek.Day())

	var organization string
	var namePattern string
	var sinceText string
	flag.StringVar(&organization, "org", "", "Github organization to scan")
	flag.StringVar(&namePattern, "name", "*", "Regex pattern to match the repository name")
	flag.StringVar(&sinceText, "since", sinceDefault, "time span to look into")
	flag.Parse()

	since, err := time.Parse("2006-01-02", sinceText)
	if err != nil {
		panic(err)
	}

	if organization == "" {
		fmt.Println("Please provide an Github organization name with --org")
		os.Exit(1)
	}

	return &Options{
		Since:        since,
		Organization: organization,
		NamePattern:  namePattern,
	}
}

func showResults(statsPerUser map[string]*Stats) {
	t := table.NewWriter()
	t.SetTitle("Code Review Leaderboard")
	t.AppendHeader(table.Row{"User", "Pull Requests", "Reviews", "Comments", "#Comment Lines"})

	for _, stats := range statsPerUser {
		t.AppendRow(table.Row{stats.Name, stats.PullRequests, stats.Reviews, stats.Comments, stats.CommentLinesWritten})
	}

	fmt.Println()
	fmt.Println(t.Render())
}

func main() {
	pw := progress.NewWriter()
	pw.SetSortBy(progress.SortByPercentDsc)
	pw.SetStyle(progress.StyleDefault)
	pw.Style().Colors = progress.StyleColorsDefault
	go pw.Render()

	initializedLogger()

	options := parseCliArgs()

	client := createClient()

	repos := make(chan *github.Repository, 128)
	go fetchAllRepositories(client, options, repos, 5)

	stats := make(chan *Stats, 128)
	go processRepositories(client, pw, options, repos, stats)

	accumulated := accumulateStatsPerUser(stats)

	// wait until progress bar rendering is done
	for pw.LengthActive() != 0 {
	}
	fmt.Printf("%d requests sent to Github\n", amountOfRequests.Load())
	fmt.Printf("Rate Limit: remaining=%d reset=%v\n", ratelimitRemaining.Load(), ratelimitReset.Load())
	showResults(accumulated)
}
