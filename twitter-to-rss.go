package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/gorilla/feeds"
	"golang.org/x/oauth2"
)

const (
	BearerTokenEnv  = "TWITTER_BEARER_TOKEN"
	FeedTitle       = "Atlas Obscura"
	FeedUrl         = "https://example.com" // FIXME
	FeedDescription = "Atlas Obscura Tweets"
	FeedAuthor      = "Venky"
	FeedAuthorEmail = "venkytv@gmail.com"
)

type FeedItem struct {
	Title   string
	Url     string
	Created time.Time
}

func genFeed(items []FeedItem) *feeds.Feed {
	feed := &feeds.Feed{
		Title:       FeedTitle,
		Link:        &feeds.Link{Href: FeedUrl},
		Description: FeedDescription,
		Author:      &feeds.Author{Name: FeedAuthor, Email: FeedAuthorEmail},
		Created:     time.Now(),
	}
	for _, item := range items {
		feed.Add(&feeds.Item{
			Title:   item.Title,
			Link:    &feeds.Link{Href: item.Url},
			Created: item.Created,
		})
	}

	return feed
}

func main() {
	ctx := context.Background()
	token, ok := os.LookupEnv(BearerTokenEnv)
	if !ok {
		log.Fatalf("Env var not set: %s", BearerTokenEnv)
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
	utm_re := regexp.MustCompile(`\?utm_.*$`)

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

		resp, err := http.Get(url)
		if err != nil {
			log.Fatal("http.Get => %v", err.Error())
		}
		finalUrl := utm_re.ReplaceAllString(
			resp.Request.URL.String(), "")
		fmt.Printf("%s %s\n", text, finalUrl)

		createdAt, err := message.CreatedAtTime()
		if err != nil {
			log.Fatal(err)
		}
		feedItems = append(feedItems, FeedItem{
			Title:   text,
			Url:     finalUrl,
			Created: createdAt,
		})
	}

	feed := genFeed(feedItems)
	fmt.Println(feed.ToAtom())
}
