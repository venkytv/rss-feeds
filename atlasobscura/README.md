## Example

Atlas Obscura Twitter feed as an RSS feed.
Needs a [Twitter Developer Account](https://developer.twitter.com/en/docs/getting-started) and a [Bearer Token](https://developer.twitter.com/en/docs/authentication/oauth-2-0/bearer-tokens).

```bash
docker run -e TWITTER_BEARER_TOKEN \
	   -p 8080:8080 \
	   venkytv/rss-atlasobscura:latest
```
