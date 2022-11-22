# TwitToo

TwitToo (Tweet To): Twitter to Mastodon forwarder service!
Sends Tweets and media from Twitter to Mastodon!

## Why?

TwitToo is made to solve one of the problems with migration from Twitter, is
that many people don't want to miss out on people they follow or want to make it
easier for people that do follow them to be able to view their content on a new
platform.

In plain english *Easy dual posting to Mastodon and Twitter*.

# How to Use

Generate an application on [Twitter](https://developer.twitter.com) and also on
your preferred Mastodon instance *(Preferences > Development > New Application)*.
*The Mastodon application needs "write:statuses" and "write:media" scopes"*

## Configuration

Create a config file with the details, like below:*(Fillling in any "<>")*
*Multiple Twitter users can be added via the "users"."<twitter_username>" key*
*with the user's Mastodon account details.*

```json
{
    "log": {
        "level": 2
    },
    "twitter": {
        "username": "<twitter_username_no_@>",
        "consumer_key": "<twitter_consumer_key>",
        "consumer_secret": "<twitter_consumer_secret>",
        "access_key": "<twitter_access_key>",
        "access_secret": "<twitter_access_secret>"
    },
    "users": {
        "twitter_username": {
            "ignore_cw": false,
            "unlisted_word": "<word_to_unlist>",
            "server": "<server_url_or_dns_name>",
            "client_key": "<mastodon_client_key>",
            "client_secret": "<mastodon_client_secret>",
            "user_token": "<mastodon_user_token>"
        }
    }
}
```

The `ignore_cw` setting, when set to the default `false` value, will instruct TwitToo
to parse out any message that uses `CW: Warning` (ending with a newline) and will
mark it as sensitive (hidden) with the "Warning" text as the content warning text.

ie: This Tweet text will create a Mastodon post with the CW "Testing":

```text
CW: Testing
This is message text that is NOT parsed!
Same here!
```

-or-

```text
CW:Testing
This is message text that is NOT parsed!
Same here!
```

The `unlist_word` setting, when non-empty, will be a string that if the Tweet
text starts with, this will mark the Mastodon post as unlisted (won't pop up in
the local/federated feed). This can be a sentence, an emoji or even a single
character.

## Building

Building is easy, just run `bash build.sh` and it will create the application
in the `bin` folder as `twittoo`.

## Running

Once saved, all you have to do is run the application using `./bin/twitoo <config_file_path>`
and you're all set! Just let the application do it's thing.
