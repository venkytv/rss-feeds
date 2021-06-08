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

	twitter "github.com/g8rswimmer/go-twitter"
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
	Tweets    []twitter.TweetObj
	FeedItems []FeedItem
}

func (reader mockTweetReader) getTweets(context.Context) ([]twitter.TweetObj, error) {
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
			FeedURL,
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
			Tweets:    []twitter.TweetObj{},
			FeedItems: []FeedItem{},
		}
		feedItems, err := fetchFeedItems(ctx, reader)
		assert.Nil(t, err)
		assert.Equal(t, reader.FeedItems, feedItems)
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
				CreatedAt: "2021-05-23T19:30:00+02:00",
			},
			{
				Text:      "No URL",
				URL:       "",
				CreatedAt: "2021-05-22T09:15:00+00:00",
			},
		}

		tweets := make([]twitter.TweetObj, len(data))
		feedItems := make([]FeedItem, 0)
		for i, v := range data {
			text := v.Text
			if v.URL != "" {
				text += " " + v.URL
			}

			tweets[i] = twitter.TweetObj{
				Text:      text,
				CreatedAt: v.CreatedAt,
			}

			if v.URL != "" {
				createdAt, err := time.Parse(time.RFC3339, v.CreatedAt)
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
		feedItems, err := fetchFeedItems(ctx, reader)
		assert.Nil(t, err)
		assert.Equal(t, reader.FeedItems, feedItems)
	})
}

func TestFetchCachedFeed(t *testing.T) {
	ctx := context.Background()

	cacheTime, err := time.Parse(time.RFC3339, "2021-05-23T22:51:39+02:00")
	if err != nil {
		t.Fatal(err)
	}
	feedConfig := FeedConfig{
		Cache:             cache.New(0, 0),
		CacheTimeOverride: cacheTime,
	}

	reader := mockTweetReader{
		Tweets: []twitter.TweetObj{
			{
				Text:      "This Dalecarlian horse is about the size of a pinhead. https://t.co/IhCehLoHO3",
				CreatedAt: "2021-05-02T16:00:26+02:00",
			},
		},
	}
	bytes, err := ioutil.ReadFile("testdata/cached_feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	wantFeed := strings.TrimSuffix(string(bytes), "\n")
	cachedFeed := fetchCachedFeed(ctx, reader, feedConfig)
	assert.Equal(t, wantFeed, cachedFeed)

	time.Sleep(1 * time.Second)
	cachedFeed = fetchCachedFeed(ctx, reader, feedConfig)
	assert.Equal(t, wantFeed, cachedFeed)
}

func TestMain(m *testing.M) {
	// Skip log messages during testing
	log.SetOutput(ioutil.Discard)
	os.Exit(m.Run())
}
