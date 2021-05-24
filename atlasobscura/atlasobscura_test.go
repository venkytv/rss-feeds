package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
)

type TestFeedItem struct {
	Title    string    `json:"title"`
	Url      string    `json:"url"`
	Created  time.Time `json:"created"`
	FixedUrl string    `json:"fixedUrl"`
}

type mockTweetReader struct {
	Tweets    []twitter.Tweet
	FeedItems []FeedItem
}

func (reader mockTweetReader) getTweets(context.Context) ([]twitter.Tweet, error) {
	return reader.Tweets, nil
}

func TestFixAllUrls(t *testing.T) {
	ctx := context.Background()
	feedItems := make([]FeedItem, 0)
	var err error

	t.Run("EmptyFeed", func(t *testing.T) {
		feedItems = nil
		feedItems, err = fixAllUrls(ctx, feedItems)
		assert.Nil(t, err)
	})

	t.Run("NormalFeed", func(t *testing.T) {
		datafile, err := os.Open("testdata/feeditems.json")
		if err != nil {
			t.Fatal(err)
		}
		defer datafile.Close()

		var items []TestFeedItem
		bytes, err := ioutil.ReadAll(datafile)
		assert.Nil(t, err)
		err = json.Unmarshal(bytes, &items)
		if err != nil {
			t.Fatal(err)
		}

		feedItems = make([]FeedItem, 0)
		wantItems := make([]FeedItem, 0)
		for _, item := range items {
			feedItems = append(feedItems, FeedItem{
				title:   item.Title,
				url:     item.Url,
				created: item.Created,
			})
			wantItems = append(wantItems, FeedItem{
				title:   item.Title,
				url:     item.FixedUrl,
				created: item.Created,
			})
		}
		feedItems, err = fixAllUrls(ctx, feedItems)
		assert.Nil(t, err)
		assert.Len(t, feedItems, 3)
		assert.Equal(t, wantItems, feedItems)

		feed, err := genFeed(feedItems,
			FeedUrl("http://example.com"),
			time.Date(2021, time.May, 2, 15, 0, 0, 0, time.UTC),
		)
		assert.Nil(t, err)
		bytes, err = ioutil.ReadFile("testdata/feed.xml")
		if err != nil {
			t.Fatal(err)
		}
		wantFeed := strings.TrimSuffix(string(bytes), "\n")
		assert.Equal(t, wantFeed, feed)
	})
}

func TestFetchFeedItems(t *testing.T) {
	ctx := context.Background()

	t.Run("EmptyReader", func(t *testing.T) {
		reader := mockTweetReader{
			Tweets:    []twitter.Tweet{},
			FeedItems: []FeedItem{},
		}

		assert.Equal(t, reader.FeedItems, fetchFeedItems(ctx, reader))
	})

	t.Run("NormalReader", func(t *testing.T) {
		data := []struct {
			Text      string
			URL       string
			CreatedAt string
		}{
			{
				Text:      "Foo",
				URL:       "http://example.com",
				CreatedAt: "Sun May 23 19:30:00 +0200 2021",
			},
			{
				Text:      "No URL",
				URL:       "",
				CreatedAt: "Sat May 22 09:15:00 +0000 2021",
			},
		}

		tweets := make([]twitter.Tweet, len(data))
		feedItems := make([]FeedItem, 0)
		for i, v := range data {
			text := v.Text
			if v.URL != "" {
				text += " " + v.URL
			}

			tweets[i] = twitter.Tweet{
				Text:      text,
				CreatedAt: v.CreatedAt,
			}

			if v.URL != "" {
				createdAt, err := time.Parse(time.RubyDate, v.CreatedAt)
				if err != nil {
					t.Fatal(err)
				}
				feedItems = append(feedItems, FeedItem{
					title:   v.Text,
					url:     v.URL,
					created: createdAt,
				})
			}

		}

		reader := mockTweetReader{
			Tweets:    tweets,
			FeedItems: feedItems,
		}
		assert.Equal(t, reader.FeedItems, fetchFeedItems(ctx, reader))
	})
}

func TestFetchCachedFeed(t *testing.T) {
	cacheTime, err := time.Parse(time.RFC3339, "2021-05-23T22:51:39+02:00")
	if err != nil {
		t.Fatal(err)
	}
	feedConfig := FeedConfig{
		Url:               "http://example.com",
		Cache:             cache.New(0, 0),
		CacheTimeOverride: cacheTime,
	}

	reader := mockTweetReader{
		Tweets: []twitter.Tweet{
			{
				Text:      "This Dalecarlian horse is about the size of a pinhead. https://t.co/IhCehLoHO3",
				CreatedAt: "Sun May 02 16:00:26 +0200 2021",
			},
		},
	}
	bytes, err := ioutil.ReadFile("testdata/cached_feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	wantFeed := strings.TrimSuffix(string(bytes), "\n")
	cachedFeed := fetchCachedFeed(reader, feedConfig)
	assert.Equal(t, wantFeed, cachedFeed)

	time.Sleep(1 * time.Second)
	cachedFeed = fetchCachedFeed(reader, feedConfig)
	assert.Equal(t, wantFeed, cachedFeed)
}

func TestMain(m *testing.M) {
	// Skip log messages during testing
	log.SetOutput(ioutil.Discard)
	os.Exit(m.Run())
}
