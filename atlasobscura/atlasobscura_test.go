package main

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"
)

type TestFeedItem struct {
	Title    string    `json:"title"`
	Url      string    `json:"url"`
	Created  time.Time `json:"created"`
	FixedUrl string    `json:"fixedUrl"`
}

func TestFixAllUrls(t *testing.T) {
	feedItems := make([]FeedItem, 0)
	var err error

	t.Run("EmptyFeed", func(t *testing.T) {
		feedItems = nil
		feedItems, err = fixAllUrls(feedItems)
		assert.Nil(t, err)
	})

	t.Run("NormalFeed", func(t *testing.T) {
		datafile, err := os.Open("testdata/feeditems.json")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer datafile.Close()

		var items []TestFeedItem
		bytes, err := ioutil.ReadAll(datafile)
		assert.Nil(t, err)
		err = json.Unmarshal(bytes, &items)
		if err != nil {
			t.Fatalf("%v", err)
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
		feedItems, err = fixAllUrls(feedItems)
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
			t.Fatalf("%v", err)
		}
		wantFeed := strings.TrimSuffix(string(bytes), "\n")
		assert.Equal(t, wantFeed, feed)
	})
}
