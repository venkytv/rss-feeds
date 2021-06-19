package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
)

func TestGetTopStories(t *testing.T) {
	// Mock server for the Hacker News story list API endpoint
	storyListSrv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			bytes, err := ioutil.ReadFile("testdata/story-list.json")
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes)
		}))
	defer storyListSrv.Close()

	// Mock server for the Hacker News story details API endpoint
	url_re := regexp.MustCompile(`/(\d+\.json)$`)
	storySrv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tokens := url_re.FindStringSubmatch(r.URL.Path)
			if len(tokens) < 1 {
				t.Fatal("Failed to find story ID in URL: ", r.URL)
			}
			bytes, err := ioutil.ReadFile("testdata/" + tokens[1])
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes)
		}))
	defer storySrv.Close()

	api := HackerNewsAPI{
		StoryList: storyListSrv.URL,
		Story:     storySrv.URL + "/%d.json",
	}

	cacheTime, err := time.Parse(time.RFC3339, "2021-05-25T10:29:48+02:00")
	feedConfig := FeedConfig{
		Cache:             cache.New(0, 0),
		CacheTimeOverride: cacheTime,
	}

	bytes, err := ioutil.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	wantFeed := strings.TrimSuffix(string(bytes), "\n")

	t.Run("FetchFeed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		storyHandler(api, feedConfig).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := rr.Result()
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, wantFeed, string(body))
	})

	t.Run("FetchCachedFeed", func(t *testing.T) {
		/*
		 * Shut down the story details lookup mock API endpoint.
		 * This ensures that any successful lookups are from the cache.
		 */
		storySrv.Close()

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		storyHandler(api, feedConfig).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := rr.Result()
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, wantFeed, string(body))
	})
}

func TestUnrollTwitterThread(t *testing.T) {
	URL := "https://twitter.com/BrantlyMillegan/status/1402388133086367751"
	unrolledURL := "https://threadreaderapp.com/thread/1402388133086367751.html"
	stories := []Story{
		{
			ID:        123,
			By:        "Someone",
			Score:     100,
			Timestamp: 1624096448,
			URL:       URL,
		},
	}
	stories = unrollTwitterThread(stories)
	assert.Equal(t, unrolledURL, stories[0].URL)
}

func TestMain(m *testing.M) {
	// Skip log messages during testing
	log.SetOutput(ioutil.Discard)
	os.Exit(m.Run())
}
