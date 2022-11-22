// Copyright 2021 - 2022 PurpleSec Team
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//

package twittoo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/McKael/madon"
	"github.com/PurpleSec/logx"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
)

const pause = time.Second * 5

var (
	found        struct{}
	statusParams = &twitter.StatusShowParams{TweetMode: "extended", IncludeEntities: twitter.Bool(true), IncludeMyRetweet: twitter.Bool(true)}
)

// Service represents a single instance of TwitToo. This can be created by the
// 'New' function.
type Service struct {
	log   logx.Log
	err   error
	twit  *twitter.Client
	http  *http.Client
	users map[string]serviceUser
	tmp   string
}
type serviceUser struct {
	client  *madon.Client
	private string
	ignore  bool
}
type serviceConfig struct {
	Twitter struct {
		AccessKey      string `json:"access_key"`
		ConsumerKey    string `json:"consumer_key"`
		AccessSecret   string `json:"access_secret"`
		ConsumerSecret string `json:"consumer_secret"`
	} `json:"twitter"`
	Users map[string]struct {
		Key    string `json:"client_key"`
		Token  string `json:"user_token"`
		Secret string `json:"client_secret"`
		Server string `json:"server"`
		UnList string `json:"unlisted_word"`
		Ignore bool   `json:"ignore_cw"`
	} `json:"users"`
	Log struct {
		File  string `json:"file"`
		Level uint   `json:"level"`
	} `json:"log"`
}

// Run starts the service and listens for any incoming requests. This function
// will block unless stopped by an interrupt (Ctrl-C).
//
// If any errors occur during start or runtime, the will be returned by this
// function when closing.
func (s *Service) Run() error {
	var (
		w    = make(chan os.Signal, 1)
		m    = make(chan *twitter.Tweet)
		x, f = context.WithCancel(context.Background())
		g    sync.WaitGroup
	)
	signal.Notify(w, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go s.twitter(x, &g, m)
	go s.mastodon(x, &g, m)
	select {
	case <-w:
	case <-x.Done():
	}
	f()
	os.RemoveAll(s.tmp)
	g.Wait()
	signal.Stop(w)
	close(w)
	close(m)
	return s.err
}
func (c serviceConfig) verify() error {
	if len(c.Users) == 0 {
		return errors.New(`"users" cannot be empty`)
	}
	for k, v := range c.Users {
		if len(k) == 0 {
			return errors.New(`"users" cannot contain an empty username`)
		}
		if len(v.Key) == 0 {
			return errors.New(`"users.` + k + `.client_key" cannot be empty`)
		}
		if len(v.Token) == 0 {
			return errors.New(`"users.` + k + `.user_token" cannot be empty`)
		}
		if len(v.Secret) == 0 {
			return errors.New(`"users.` + k + `.client_secret" cannot be empty`)
		}
		if len(v.Server) == 0 {
			return errors.New(`"users.` + k + `.server" cannot be empty`)
		}
	}
	if len(c.Twitter.AccessKey) == 0 {
		return errors.New(`"twitter.access_key" cannot be empty`)
	}
	if len(c.Twitter.ConsumerKey) == 0 {
		return errors.New(`"twitter.consumer_key" cannot be empty`)
	}
	if len(c.Twitter.AccessSecret) == 0 {
		return errors.New(`"twitter.access_secret" cannot be empty`)
	}
	if len(c.Twitter.ConsumerSecret) == 0 {
		return errors.New(`"twitter.consumer_secret" cannot be empty`)
	}
	return nil
}

// New creates and preforms setup/account verification on the supplied configuration
// before returning.
//
// The only argument is a file path to a configuration file in JSON format.
//
// Any errors or issues will return an error and will prevent the Service from
// being started.
func New(file string) (*Service, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New(`cannot open "` + file + `": ` + err.Error())
	}
	var c serviceConfig
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, errors.New(`cannot parse "` + file + `": ` + err.Error())
	}
	if err = c.verify(); err != nil {
		return nil, errors.New(`config "` + file + `" is invalid: ` + err.Error())
	}
	s := &Service{tmp: os.TempDir() + string(os.PathSeparator) + "twittoo", users: make(map[string]serviceUser)}
	if v, err1 := os.Stat(s.tmp); err1 != nil {
		if err1 = os.MkdirAll(s.tmp, 0750); err1 != nil {
			return nil, errors.New(`cannot make temp directory "` + s.tmp + `": ` + err1.Error())
		}
	} else if !v.IsDir() {
		return nil, errors.New(`temp directory given "` + s.tmp + `" was not a directory`)
	}
	if s.log = logx.Console(logx.NormalUint(c.Log.Level, logx.Info)); len(c.Log.File) > 0 {
		f, err1 := logx.File(c.Log.File, logx.NormalUint(c.Log.Level, logx.Info), logx.Append)
		if err1 != nil {
			return nil, err1
		}
		s.log = logx.Multiple(s.log, f)
	}
	s.twit = twitter.NewClient(
		oauth1.NewConfig(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret).Client(
			context.Background(), oauth1.NewToken(c.Twitter.AccessKey, c.Twitter.AccessSecret),
		),
	)
	if _, _, err = s.twit.Accounts.VerifyCredentials(nil); err != nil {
		return nil, errors.New("twitter login failed: " + err.Error())
	}
	e := make([]string, 0, len(c.Users))
	for k := range c.Users {
		if len(k) == 0 { // Shouldn't happen
			continue
		}
		e = append(e, k)
	}
	u, _, err := s.twit.Users.Lookup(&twitter.UserLookupParams{ScreenName: e})
	if err != nil {
		return nil, errors.New("twitter user lookup failed: " + err.Error())
	}
	for k, v := range c.Users {
		if len(k) == 0 {
			continue
		}
		var j string
		for i := range u {
			if strings.EqualFold(k, u[i].ScreenName) {
				j = u[i].IDStr
				break
			}
		}
		if len(j) == 0 {
			return nil, errors.New(`twitter user "` + k + `" lookup did not return`)
		}
		z, err := madon.RestoreApp("TwitToo", v.Server, v.Key, v.Secret, &madon.UserToken{AccessToken: v.Token, Scope: "write:media write:statuses"})
		if err != nil {
			return nil, errors.New(`mastodon login for "` + k + `" failed: "` + err.Error())
		}
		s.users[j] = serviceUser{client: z, ignore: v.Ignore, private: v.UnList}
	}
	s.http = &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: pause, KeepAlive: time.Second * 15}).DialContext,
		MaxIdleConns:          64,
		IdleConnTimeout:       pause,
		TLSHandshakeTimeout:   pause,
		ExpectContinueTimeout: pause,
		ResponseHeaderTimeout: pause,
	}}
	return s, nil
}
func fullTweetText(t *twitter.Tweet) string {
	s := t.FullText
	switch {
	case t.ExtendedTweet != nil && len(t.ExtendedTweet.FullText) > 0:
		s = t.ExtendedTweet.FullText
	case len(t.FullText) == 0:
		s = t.Text
	}
	// Remove Twitter Tracking URLS
	if t.Entities != nil && len(t.Entities.Urls) > 0 {
		for i := range t.Entities.Urls {
			s = strings.ReplaceAll(s, t.Entities.Urls[i].URL, t.Entities.Urls[i].ExpandedURL)
		}
	}
	if t.ExtendedTweet != nil && t.ExtendedTweet.Entities != nil && len(t.ExtendedTweet.Entities.Urls) > 0 {
		for i := range t.ExtendedTweet.Entities.Urls {
			s = strings.ReplaceAll(s, t.ExtendedTweet.Entities.Urls[i].URL, t.ExtendedTweet.Entities.Urls[i].ExpandedURL)
		}
	}
	return s
}
func parseWarns(s string) (string, bool, string) {
	if len(s) < 2 {
		return s, false, ""
	}
	v := strings.TrimSpace(s)
	if v[0] != 'C' || v[1] != 'W' || v[2] != ':' {
		return s, false, ""
	}
	i := strings.IndexByte(v, '\n')
	if i < 6 {
		return s, false, ""
	}
	return v[i+1:], true, strings.TrimSpace(v[3:i])
}
func (s Service) streamParams() *twitter.StreamFilterParams {
	u := make([]string, 0, len(s.users))
	for k := range s.users {
		u = append(u, k)
	}
	return &twitter.StreamFilterParams{FilterLevel: "none", Follow: u, Language: []string{"en"}, StallWarnings: twitter.Bool(true)}
}
func (s *Service) parseMedia(u serviceUser, t *twitter.Tweet) []int64 {
	a := make(map[string]struct{}, 16)
	if t.Entities != nil && len(t.Entities.Media) > 0 {
		for i := range t.Entities.Media {
			if len(t.Entities.Media[i].MediaURLHttps) == 0 || t.Entities.Media[i].Type != "photo" {
				continue
			}
			a[t.Entities.Media[i].MediaURLHttps] = found
		}
	}
	if t.ExtendedEntities != nil && len(t.ExtendedEntities.Media) > 0 {
		for i := range t.ExtendedEntities.Media {
			if len(t.ExtendedEntities.Media[i].MediaURLHttps) == 0 || t.ExtendedEntities.Media[i].Type != "photo" {
				continue
			}
			a[t.ExtendedEntities.Media[i].MediaURLHttps] = found
		}
	}
	if len(a) == 0 {
		return nil
	}
	s.log.Debug(`Tweet "twitter.com/%s/status/%s" has %d attachments.`, t.User.ScreenName, t.IDStr, len(a))
	var (
		d = make([]string, 0, len(a))
		n int
	)
	for i := range a {
		r, err := s.http.Get(i)
		if err != nil {
			s.log.Error(`Could not download Tweet "twitter.com/%s/status/%s" media "%s": %s!`, t.User.ScreenName, t.IDStr, i, err.Error())
			continue
		}
		var (
			p = filepath.Join(s.tmp, strconv.Itoa(n)+".png")
			f *os.File
		)
		if f, err = os.OpenFile(p, 0x242, 0640); err == nil {
			_, err = io.Copy(f, r.Body)
			if f.Close(); err == nil {
				s.log.Debug(`Saved Tweet "twitter.com/%s/status/%s" media to "%s" file "%s".`, t.User.ScreenName, t.IDStr, i, p)
				d = append(d, p)
				n++
			} else {
				s.log.Error(`Could not save Tweet "twitter.com/%s/status/%s" media "%s" to "%s": %s!`, t.User.ScreenName, t.IDStr, i, p, err.Error())
				os.Remove(p)
			}
		} else {
			s.log.Error(`Could not save Tweet "twitter.com/%s/status/%s" media "%s" to "%s": %s!`, t.User.ScreenName, t.IDStr, i, p, err.Error())
		}
		r.Body.Close()
		r.Body = nil
		r = nil
	}
	a = nil
	s.log.Debug(`Uploading %d attachments for Tweet "twitter.com/%s/status/%s"..`, len(d), t.User.ScreenName, t.IDStr)
	z := make([]int64, 0, len(d))
	for i := range d {
		x, err := u.client.UploadMedia(d[i], "", "")
		if os.Remove(d[i]); err != nil {
			s.log.Error(`Could not upload Tweet "twitter.com/%s/status/%s" media file "%s": %s!`, t.User.ScreenName, t.IDStr, d[i], err.Error())
			continue
		}
		s.log.Debug(`Created attachment ID %d from file "%s"!`, x.ID, d[i])
		z = append(z, x.ID)
	}
	return z
}
func (s *Service) twitter(x context.Context, g *sync.WaitGroup, c chan<- *twitter.Tweet) {
	s.log.Info("Starting Twitter stream thread..")
	var w *twitter.Stream
	if w, s.err = s.twit.Streams.Filter(s.streamParams()); s.err != nil {
		s.log.Error("Twitter stream setup failed: %s!", s.err.Error())
		return
	}
loop:
	for g.Add(1); ; {
		select {
		case n, ok := <-w.Messages:
			switch s.log.Trace("Received Message (%T/%t): %+v\n", n, ok, n); t := n.(type) {
			case *twitter.Tweet:
				if _, ok1 := s.users[t.User.IDStr]; !ok1 {
					s.log.Debug(`Skipping Tweet from non-set user ID "%s"..`, t.IDStr)
					break
				}
				if t.Retweeted || t.RetweetedStatus != nil {
					s.log.Debug(`Tweet "twitter.com/%s/status/%s" is a retweet, skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				if t.Text[0] == '@' && t.InReplyToUserID != t.User.ID {
					s.log.Debug(`Tweet "twitter.com/%s/status/%s" is a direct reply, skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				if t.QuotedStatusID != 0 && t.QuotedStatus != nil && t.QuotedStatus.User.IDStr != t.User.IDStr {
					s.log.Debug(`Tweet "twitter.com/%s/status/%s" is a quoted retweet skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				// Get extended Tweet Info.
				if v, _, err := s.twit.Statuses.Show(t.ID, statusParams); err == nil {
					c <- v
					break
				}
				c <- t
			case *twitter.StreamLimit:
				s.log.Warning("Twitter stream thread received a StreamLimit message of %d!", t.Track)
			case *twitter.StallWarning:
				s.log.Warning("Twitter stream thread received a StallWarning message: %s!", t.Message)
			case *twitter.StreamDisconnect:
				s.log.Error("Twitter stream thread received a StreamDisconnect message: %s!", t.Reason)
				w.Stop()
				s.log.Info("Waiting %s before retrying..", pause.String())
				time.Sleep(pause)
				if w, s.err = s.twit.Streams.Filter(s.streamParams()); s.err != nil {
					s.log.Error("Twitter stream reload failed: %s!", s.err.Error())
					break loop
				}
			case *twitter.Event, *twitter.FriendsList, *twitter.UserWithheld, *twitter.DirectMessage, *twitter.StatusDeletion, *twitter.StatusWithheld, *twitter.LocationDeletion:
			default:
				if !ok {
					s.log.Error("Twitter stream thread received a channel closure, attempting to reload!")
					w.Stop()
					s.log.Info("Waiting %s before retrying..", pause.String())
					time.Sleep(pause)
					if w, s.err = s.twit.Streams.Filter(s.streamParams()); s.err != nil {
						s.log.Error("Twitter stream reload failed: %s!", s.err.Error())
						break loop
					}
				} else if t != nil {
					s.log.Warning("Twitter stream thread received an unrecognized message (%T): %s\n", t, t)
				}
			}
		case <-x.Done():
			break loop
		}
	}
	if s.log.Info("Stopped Twitter stream thread."); w != nil {
		w.Stop()
	}
	g.Done()
}
func (s *Service) mastodon(x context.Context, g *sync.WaitGroup, c <-chan *twitter.Tweet) {
	s.log.Info("Starting Mastodon sender thread..")
loop:
	for g.Add(1); ; {
		select {
		case t := <-c:
			s.log.Debug(`Received Tweet "twitter.com/%s/status/%s" to send!`, t.User.ScreenName, t.IDStr)
			u, ok := s.users[t.User.IDStr]
			if !ok {
				s.log.Warning(`Received a Tweet "twitter.com/%s/status/%s" without a matching user!`, t.User.ScreenName, t.IDStr)
				break
			}
			s.log.Debug(`Running in the context of the user "%s" (%s).`, t.User.ScreenName, t.User.IDStr)
			v, z, w, e := fullTweetText(t), t.PossiblySensitive, "", ""
			if len(u.private) > 0 && strings.HasPrefix(v, u.private) {
				v, e = strings.TrimSpace(v[len(u.private):]), "unlisted"
			}
			if !u.ignore {
				v, z, w = parseWarns(v)
			}
			p, err := u.client.PostStatus(v, 0, s.parseMedia(u, t), z, w, e)
			if err != nil {
				s.log.Error("Failed to post Mastodon status: %s!", err.Error())
				break
			}
			s.log.Info(`Posted Mastodon status %d "%s"!`, p.ID, p.URL)
		case <-x.Done():
			break loop
		}
	}
	g.Done()
}
