package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/gorilla/feeds"
	"github.com/patrickmn/go-cache"
	"golang.org/x/oauth2"
)

const (
	BearerTokenEnv  = "TWITTER_BEARER_TOKEN"
	FeedUrlEnv      = "FEED_URL"
	FeedTitle       = "Atlas Obscura"
	FeedDescription = "Atlas Obscura Tweets"
	FeedAuthor      = "Venky"
	FeedAuthorEmail = "venkytv@gmail.com"
	Timeout         = 10 * time.Second
	CacheInterval   = 30 * time.Minute
)

type FeedItem struct {
	title   string
	url     string
	created time.Time
}

type FeedUrl string

type tweetReader interface {
	getTweets(context.Context) ([]twitter.Tweet, error)
}

var utm_re = regexp.MustCompile(`\?utm_.*$`)

func genFeed(items []FeedItem, url FeedUrl, createTime time.Time) (string, error) {
	feed := &feeds.Feed{
		Title:       FeedTitle,
		Link:        &feeds.Link{Href: string(url)},
		Description: FeedDescription,
		Author:      &feeds.Author{Name: FeedAuthor, Email: FeedAuthorEmail},
		Created:     createTime,
	}
	for _, item := range items {
		feed.Add(&feeds.Item{
			Title:   item.title,
			Link:    &feeds.Link{Href: item.url},
			Created: item.created,
		})
	}

	atom, err := feed.ToAtom()
	if err != nil {
		return "", err
	}
	return atom, nil
}

func gen(items []FeedItem) <-chan FeedItem {
	out := make(chan FeedItem)
	go func() {
		for _, item := range items {
			out <- item
		}
		close(out)
	}()
	return out
}

type result struct {
	item FeedItem
	err  error
}

func fixer(ctx context.Context, items <-chan FeedItem, c chan<- result) {
	for item := range items {
		client := http.Client{
			Timeout: Timeout,
		}
		resp, err := client.Head(item.url)
		if err == nil {
			url := utm_re.ReplaceAllString(
				resp.Request.URL.String(), "")
			item.url = url
		}
		select {
		case c <- result{item, err}:
		case <-ctx.Done():
			return
		}
	}
}

func fixAllUrls(ctx context.Context, items []FeedItem) ([]FeedItem, error) {
	items_chan := gen(items)

	// Start a fixed number of channels to fix URLs
	c := make(chan result)
	var wg sync.WaitGroup
	const numFixers = 10
	wg.Add(numFixers)
	for i := 0; i < numFixers; i++ {
		go func() {
			fixer(ctx, items_chan, c)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(c)
	}()

	out := make([]FeedItem, 0)
	for r := range c {
		if r.err != nil {
			return nil, r.err
		}
		out = append(out, r.item)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].created.After(out[j].created)
	})

	return out, nil
}

type tweetReaderImpl struct{}

func (r tweetReaderImpl) getTweets(ctx context.Context) ([]twitter.Tweet, error) {
	token, ok := os.LookupEnv(BearerTokenEnv)
	if !ok {
		log.Fatal("Env var not set: ", BearerTokenEnv)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := twitter.NewClient(tc)

	tweets, _, err := client.Timelines.UserTimeline(
		&twitter.UserTimelineParams{ScreenName: "atlasobscura"})
	return tweets, err
}

func fetchFeedItems(ctx context.Context, reader tweetReader) []FeedItem {
	feedItems := make([]FeedItem, 0)
	tweet_re := regexp.MustCompile(`(.*?)\s(https?://.*)`)

	tweets, err := reader.getTweets(ctx)
	for _, message := range tweets {
		tweet := message.Text
		t := tweet_re.FindStringSubmatch(tweet)
		if len(t) < 2 {
			log.Println("No URL in tweet: ", tweet)
			continue
		}
		text := t[1]
		url := t[2]
		createdAt, err := message.CreatedAtTime()
		if err != nil {
			log.Fatal(err)
		}
		feedItems = append(feedItems, FeedItem{
			title:   text,
			url:     url,
			created: createdAt,
		})
	}

	feedItems, err = fixAllUrls(ctx, feedItems)
	if err != nil {
		log.Fatal(err)
	}

	return feedItems
}

func cacheFeed(reader tweetReader, feedCache *cache.Cache) {
	log.Print("Caching feed")
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel() // Cancel context once feeds are fetched

	feedUrl, ok := os.LookupEnv(FeedUrlEnv)
	if !ok {
		log.Fatal("Env var not set: ", FeedUrlEnv)
	}

	feedItems := fetchFeedItems(ctx, reader)

	feed, err := genFeed(feedItems, FeedUrl(feedUrl), time.Now())
	if err != nil {
		log.Fatal(err)
	}

	feedCache.Set("feed", feed, cache.NoExpiration)
}

func fetchCachedFeed(reader tweetReader, feedCache *cache.Cache) string {
	feed, found := feedCache.Get("feed")
	if !found {
		log.Print("Cached feed not found")
		cacheFeed(reader, feedCache)
		feed, found = feedCache.Get("feed")
	}
	return feed.(string)
}

func feedHandler(reader tweetReader, feedCache *cache.Cache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, fetchCachedFeed(reader, feedCache))
	})
}

func main() {
	reader := tweetReaderImpl{}

	// Cache feeds indefinitely
	feedCache := cache.New(0, 0)

	// Cache feed at startup
	cacheFeed(reader, feedCache)

	ticker := time.NewTicker(CacheInterval)
	defer ticker.Stop()

	done := make(chan bool)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				cacheFeed(reader, feedCache)
			}
		}
	}()

	log.Print("Starting server")
	srv := http.Server{
		Addr:         ":8080",
		ReadTimeout:  Timeout / 2.0,
		WriteTimeout: Timeout,
		Handler: http.TimeoutHandler(feedHandler(reader, feedCache),
			Timeout, "Timeout!\n"),
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v\n", err)
	}
}
