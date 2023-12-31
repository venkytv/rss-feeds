package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/feeds"
	"github.com/patrickmn/go-cache"
)

const (
	FeedURL         = "https://news.ycombinator.com/best"
	FeedTitle       = "Hacker News"
	FeedDescription = "Hacker News Top Stories"
	FeedAuthor      = "Venky"
	FeedAuthorEmail = "venkytv@gmail.com"
	StoryListURL    = "https://hacker-news.firebaseio.com/v0/beststories.json"
	StoryURL        = "https://hacker-news.firebaseio.com/v0/item/%d.json"
	HNSourceURL     = "https://news.ycombinator.com/item?id=%d"
	TwitterRE       = `^https://(?:twitter|x)\.com/(.*)`
	ThreaderURL     = "https://nitter.net/%s"
	Timeout         = 10 * time.Second
	CacheTime       = 24 * time.Hour
	RefreshInterval = 10 * time.Minute
	NumStoryLookups = 50
)

type HackerNewsAPI struct {
	StoryList string
	Story     string
}

type StoryID int

type Story struct {
	ID        StoryID
	By        string `json:"by"`
	Score     int    `json:"score"`
	Timestamp int64  `json:"time"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Text      string `json:"text"`
}

func (s Story) Time() time.Time {
	return time.Unix(s.Timestamp, 0)
}

type FeedConfig struct {
	Cache             *cache.Cache
	CacheTimeOverride time.Time // Override for testing
}

func (f FeedConfig) CacheTime() time.Time {
	feedTime := f.CacheTimeOverride
	if feedTime.IsZero() {
		feedTime = time.Now()
	}
	return feedTime
}

func getStoryFromCache(api HackerNewsAPI, id StoryID, storyCache *cache.Cache) (Story, error) {
	idStr := strconv.Itoa(int(id))
	story, found := storyCache.Get(idStr)
	if !found {
		log.Print("Fetching story ", id)
		var err error
		story, err = getStory(api, id)
		if err != nil {
			return Story{}, err
		}
		storyCache.Set(idStr, story, cache.NoExpiration)
	}
	return story.(Story), nil
}

func getStory(api HackerNewsAPI, id StoryID) (Story, error) {
	var story Story

	client := http.Client{
		Timeout: time.Duration(Timeout),
	}
	url := fmt.Sprintf(api.Story, id)
	resp, err := client.Get(url)
	if err != nil {
		return Story{}, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Story{}, err
	}

	json.Unmarshal(body, &story)
	story.ID = id
	return story, nil
}

func getTopStoryIDs(api HackerNewsAPI) ([]StoryID, error) {
	client := http.Client{
		Timeout: time.Duration(Timeout),
	}
	resp, err := client.Get(api.StoryList)
	if err != nil {
		return []StoryID{}, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []StoryID{}, err
	}

	topStories := make([]StoryID, 0)
	json.Unmarshal(body, &topStories)

	sort.Slice(topStories, func(i, j int) bool {
		return topStories[i] < topStories[j]
	})
	return topStories, nil
}

func getStories(api HackerNewsAPI, ids []StoryID, storyCache *cache.Cache) ([]Story, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// Pump the list of story IDs into a channel
	id_chan := make(chan StoryID)
	go func() {
		for _, id := range ids {
			id_chan <- id
		}
		close(id_chan)
	}()

	type StoryLookup struct {
		Story Story
		Error error
	}

	story_chan := make(chan StoryLookup)

	// Start a fixed number of consumers
	var wg sync.WaitGroup
	wg.Add(NumStoryLookups)
	for i := 0; i < NumStoryLookups; i++ {
		go func() {
			for id := range id_chan {
				s, err := getStoryFromCache(api, id, storyCache)

				select {
				case story_chan <- StoryLookup{s, err}:
				case <-ctx.Done():
					return
				}
			}
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(story_chan)
	}()

	stories := make([]Story, 0)
	for s := range story_chan {
		if s.Error != nil {
			return nil, s.Error
		}
		stories = append(stories, s.Story)
	}

	sort.Slice(stories, func(i, j int) bool {
		return stories[i].Timestamp > stories[j].Timestamp
	})

	return stories, nil
}

func getTopStories(api HackerNewsAPI, storyCache *cache.Cache) ([]Story, error) {
	ids, err := getTopStoryIDs(api)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	stories, err := getStories(api, ids, storyCache)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	return stories, nil
}

func unrollTwitterThread(stories []Story) []Story {
	re, err := regexp.Compile(TwitterRE)
	if err != nil {
		log.Println(err)
		return stories
	}
	for idx, _ := range stories {
		m := re.FindStringSubmatch(stories[idx].URL)
		if len(m) > 0 {
			stories[idx].URL = fmt.Sprintf(ThreaderURL, m[1])
		}
	}
	return stories
}

func storyHandler(api HackerNewsAPI, feedConfig FeedConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		stories, err := getTopStories(api, feedConfig.Cache)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		stories = unrollTwitterThread(stories)

		feed := &feeds.Feed{
			Title:       FeedTitle,
			Link:        &feeds.Link{Href: FeedURL},
			Description: FeedDescription,
			Author:      &feeds.Author{Name: FeedAuthor, Email: FeedAuthorEmail},
			Created:     feedConfig.CacheTime(),
		}
		for _, story := range stories {
			link := story.URL
			source := fmt.Sprintf(HNSourceURL, story.ID)
			if link == "" {
				link = source
			}
			feed.Add(&feeds.Item{
				Title:       story.Title,
				Link:        &feeds.Link{Href: link},
				Source:      &feeds.Link{Href: source},
				Description: story.Text,
				Id:          source,
				Created:     story.Time(),
			})
		}

		atom, err := feed.ToAtom()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		io.WriteString(w, atom)
	})
}

func main() {
	api := HackerNewsAPI{
		StoryList: StoryListURL,
		Story:     StoryURL,
	}

	storyCache := cache.New(CacheTime, 2*CacheTime)
	feedConfig := FeedConfig{
		Cache: storyCache,
	}

	// Cache stories at startup
	getTopStories(api, storyCache)

	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()

	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Refresh cache
				getTopStories(api, storyCache)
			}
		}
	}()

	log.Print("Starting server")
	srv := http.Server{
		Addr:         ":8080",
		ReadTimeout:  Timeout / 2.0,
		WriteTimeout: Timeout,
		Handler: http.TimeoutHandler(storyHandler(api, feedConfig),
			Timeout, "Timeout!\n"),
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v\n", err)
	}
}
