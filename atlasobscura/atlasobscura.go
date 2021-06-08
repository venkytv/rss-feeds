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

	twitter "github.com/g8rswimmer/go-twitter"
	"github.com/gorilla/feeds"
	"github.com/patrickmn/go-cache"
)

const (
	BearerTokenEnv  = "TWITTER_BEARER_TOKEN"
	ScreenName      = "atlasobscura"
	NumTweets       = 20
	FeedURL         = "https://www.atlasobscura.com"
	FeedTitle       = "Atlas Obscura"
	FeedDescription = "Atlas Obscura Tweets"
	FeedAuthor      = "Venky"
	FeedAuthorEmail = "venkytv@gmail.com"
	Timeout         = 10 * time.Second
	CacheInterval   = 30 * time.Minute
)

type FeedItem struct {
	Title   string
	Url     string
	Created time.Time
}

type FeedConfig struct {
	Cache             *cache.Cache
	CacheTimeOverride time.Time // Override for testing
}

type tweetReader interface {
	getTweets(context.Context) ([]twitter.TweetObj, error)
}

type authorize struct {
	Token string
}

func (a authorize) Add(req *http.Request) {
	req.Header.Add("Authorization", "Bearer "+a.Token)
}

var utm_re = regexp.MustCompile(`\?utm_.*$`)

func genFeed(items []FeedItem, url string, createTime time.Time) (string, error) {
	feed := &feeds.Feed{
		Title:       FeedTitle,
		Link:        &feeds.Link{Href: string(url)},
		Description: FeedDescription,
		Author:      &feeds.Author{Name: FeedAuthor, Email: FeedAuthorEmail},
		Created:     createTime,
	}
	for _, item := range items {
		feed.Add(&feeds.Item{
			Title:   item.Title,
			Link:    &feeds.Link{Href: item.Url},
			Created: item.Created,
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

type Result struct {
	Item  FeedItem
	Error error
}

func fixer(ctx context.Context, items <-chan FeedItem, c chan<- Result) {
	for item := range items {
		client := http.Client{
			Timeout: Timeout,
		}
		resp, err := client.Head(item.Url)
		if err == nil {
			url := utm_re.ReplaceAllString(
				resp.Request.URL.String(), "")
			item.Url = url
		}
		select {
		case c <- Result{item, err}:
		case <-ctx.Done():
			return
		}
	}
}

func fixAllUrls(ctx context.Context, items []FeedItem) ([]FeedItem, error) {
	items_chan := gen(items)

	// Start a fixed number of channels to fix URLs
	c := make(chan Result)
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
		if r.Error != nil {
			return nil, r.Error
		}
		out = append(out, r.Item)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Created.After(out[j].Created)
	})

	return out, nil
}

type tweetReaderImpl struct {
	User      *twitter.User
	TweetOpts twitter.UserTimelineOpts
}

func newTweetReader(ctx context.Context) tweetReaderImpl {
	token, ok := os.LookupEnv(BearerTokenEnv)
	if !ok {
		log.Fatal("Env var not set: ", BearerTokenEnv)
	}

	user := &twitter.User{
		Authorizer: authorize{
			Token: token,
		},
		Client: http.DefaultClient,
		Host:   "https://api.twitter.com",
	}

	tweetOpts := twitter.UserTimelineOpts{
		TweetFields: []twitter.TweetField{
			twitter.TweetFieldID,
			twitter.TweetFieldSource,
			twitter.TweetFieldText,
			twitter.TweetFieldCreatedAt,
		},
		UserFields: []twitter.UserField{},
		MaxResults: NumTweets,
	}

	return tweetReaderImpl{
		User:      user,
		TweetOpts: tweetOpts,
	}
}

func (r tweetReaderImpl) getTweets(ctx context.Context) ([]twitter.TweetObj, error) {
	lookups, err := r.User.LookupUsername(ctx, []string{ScreenName},
		twitter.UserFieldOptions{})
	if err != nil {
		return nil, err
	}

	var userID string
	for u := range lookups {
		userID = u
		break
	}

	tweets, err := r.User.Tweets(ctx, userID, r.TweetOpts)
	if err != nil {
		return nil, err
	}
	return tweets.Tweets, err
}

func fetchFeedItems(ctx context.Context, reader tweetReader) ([]FeedItem, error) {
	feedItems := make([]FeedItem, 0)
	tweet_re := regexp.MustCompile(`(.*?)\s(https?://.*)`)

	tweets, err := reader.getTweets(ctx)
	if err != nil {
		return feedItems, err
	}
	for _, message := range tweets {
		tweet := message.Text
		t := tweet_re.FindStringSubmatch(tweet)
		if len(t) < 2 {
			log.Println("No URL in tweet: ", tweet)
			continue
		}
		text := t[1]
		url := t[2]

		createdAt, err := time.Parse(time.RFC3339, message.CreatedAt)
		if err != nil {
			log.Printf("Failed to parse time: %v: %v", message, err)
			continue
		}
		feedItems = append(feedItems, FeedItem{
			Title:   text,
			Url:     url,
			Created: createdAt,
		})
	}

	feedItems, err = fixAllUrls(ctx, feedItems)
	if err != nil {
		log.Fatal(err)
	}

	return feedItems, nil
}

func cacheFeed(ctx context.Context, reader tweetReader, feedConfig FeedConfig) {
	log.Print("Caching feed")
	feedItems, err := fetchFeedItems(ctx, reader)
	if err != nil {
		log.Printf("Failed to update cache: %v\n", err)
		return
	}

	feedTime := feedConfig.CacheTimeOverride
	if feedTime.IsZero() {
		feedTime = time.Now()
	}

	feed, err := genFeed(feedItems, FeedURL, feedTime)
	if err != nil {
		log.Fatal(err)
	}

	feedConfig.Cache.Set("feed", feed, cache.NoExpiration)
}

func fetchCachedFeed(ctx context.Context, reader tweetReader, feedConfig FeedConfig) string {
	feed, found := feedConfig.Cache.Get("feed")
	if !found {
		log.Print("Cached feed not found")
		cacheFeed(ctx, reader, feedConfig)
		feed, found = feedConfig.Cache.Get("feed")
	}
	return feed.(string)
}

func feedHandler(ctx context.Context, reader tweetReader, feedConfig FeedConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, fetchCachedFeed(ctx, reader, feedConfig))
	})
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel() // Cancel context once feeds are fetched

	feedConfig := FeedConfig{
		Cache: cache.New(0, 0), // Cache feeds indefinitely
	}

	reader := newTweetReader(ctx)

	// Cache feed at startup
	cacheFeed(ctx, reader, feedConfig)

	ticker := time.NewTicker(CacheInterval)
	defer ticker.Stop()

	done := make(chan bool)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				cacheFeed(ctx, reader, feedConfig)
			}
		}
	}()

	log.Print("Starting server")
	srv := http.Server{
		Addr:         ":8080",
		ReadTimeout:  Timeout / 2.0,
		WriteTimeout: Timeout,
		Handler: http.TimeoutHandler(feedHandler(ctx, reader, feedConfig),
			Timeout, "Timeout!\n"),
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v\n", err)
	}
}
