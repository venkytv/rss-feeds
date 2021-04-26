package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/gorilla/feeds"
	"golang.org/x/oauth2"
)

const (
	BearerTokenEnv  = "TWITTER_BEARER_TOKEN"
	FeedUrlEnv      = "FEED_URL"
	FeedTitle       = "Atlas Obscura"
	FeedDescription = "Atlas Obscura Tweets"
	FeedAuthor      = "Venky"
	FeedAuthorEmail = "venkytv@gmail.com"
)

type FeedItem struct {
	title   string
	url     string
	created time.Time
}

type FeedUrl string

var utm_re = regexp.MustCompile(`\?utm_.*$`)

func genFeed(items []FeedItem, url FeedUrl) *feeds.Feed {
	feed := &feeds.Feed{
		Title:       FeedTitle,
		Link:        &feeds.Link{Href: string(url)},
		Description: FeedDescription,
		Author:      &feeds.Author{Name: FeedAuthor, Email: FeedAuthorEmail},
		Created:     time.Now(),
	}
	for _, item := range items {
		feed.Add(&feeds.Item{
			Title:   item.title,
			Link:    &feeds.Link{Href: item.url},
			Created: item.created,
		})
	}

	return feed
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

func fixer(done <-chan struct{}, items <-chan FeedItem, c chan<- result) {
	for item := range items {
		resp, err := http.Get(item.url)
		if err == nil {
			url := utm_re.ReplaceAllString(
				resp.Request.URL.String(), "")
			item.url = url
		}
		select {
		case c <- result{item, err}:
		case <-done:
			return
		}
	}
}

func fixAllUrls(items []FeedItem) ([]FeedItem, error) {
	// Set up a done channel to signal completion
	done := make(chan struct{})
	defer close(done)

	items_chan := gen(items)

	// Start a fixed number of channels to fix URLs
	c := make(chan result)
	var wg sync.WaitGroup
	const numFixers = 10
	wg.Add(numFixers)
	for i := 0; i < numFixers; i++ {
		go func() {
			fixer(done, items_chan, c)
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

	return out, nil
}

func main() {
	ctx := context.Background()
	token, ok := os.LookupEnv(BearerTokenEnv)
	if !ok {
		log.Fatal("Env var not set: ", BearerTokenEnv)
	}

	feedUrl, ok := os.LookupEnv(FeedUrlEnv)
	if !ok {
		log.Fatal("Env var not set: ", FeedUrlEnv)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := twitter.NewClient(tc)

	demux := twitter.NewSwitchDemux()
	demux.Tweet = func(tweet *twitter.Tweet) {
		fmt.Println(tweet.Text)
	}

	tweet_re := regexp.MustCompile(`(.*?)\s(https?://.*)`)

	tweets, _, err := client.Timelines.UserTimeline(
		&twitter.UserTimelineParams{ScreenName: "atlasobscura"})
	if err != nil {
		log.Fatal(err)
	}

	feedItems := make([]FeedItem, 0)

	for _, message := range tweets {
		tweet := message.Text
		t := tweet_re.FindStringSubmatch(tweet)
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

	feedItems, err = fixAllUrls(feedItems)
	if err != nil {
		log.Fatal(err)
	}
	sort.Slice(feedItems, func(i, j int) bool {
		return feedItems[i].created.After(feedItems[j].created)
	})
	feed := genFeed(feedItems, FeedUrl(feedUrl))
	atom, err := feed.ToAtom()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(atom)
}
