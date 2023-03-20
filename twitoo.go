// Copyright 2021 - 2023 PurpleSec Team
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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	madon "github.com/McKael/madon/v2"
	"github.com/PurpleSec/logx"
	twitter "github.com/g8rswimmer/go-twitter/v2"
)

const pause = time.Second * 5

// Service represents a single instance of TwitToo. This can be created by the
// 'New' function.
type Service struct {
	log    logx.Log
	err    error
	twit   *twitter.Client
	users  map[string]account
	cancel context.CancelFunc
	tmp    string
	auth   string
}
type account struct {
	client  *madon.Client
	private string
	ignore  bool
}

// Run starts the service and listens for any incoming requests. This function
// will block unless stopped by an interrupt (Ctrl-C).
//
// If any errors occur during start or runtime, the will be returned by this
// function when closing.
func (s *Service) Run() error {
	if len(s.users) == 0 {
		return errors.New("no user accounts to watch")
	}
	var (
		w = make(chan os.Signal, 1)
		m = make(chan *twitter.TweetRaw)
		g sync.WaitGroup
		x context.Context
	)
	x, s.cancel = context.WithCancel(context.Background())
	signal.Notify(w, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go s.twitter(x, &g, m)
	go s.mastodon(x, &g, m)
	select {
	case <-w:
	case <-x.Done():
	}
	s.cancel()
	os.RemoveAll(s.tmp)
	g.Wait()
	signal.Stop(w)
	close(w)
	close(m)
	return s.err
}

// Add fulfils the Authenticator interface.
func (s *Service) Add(r *http.Request) {
	if len(s.auth) == 0 {
		return
	}
	r.Header.Add("Authorization", "Bearer "+s.auth)
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
	var c config
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, errors.New(`cannot parse "` + file + `": ` + err.Error())
	}
	if err = c.verify(); err != nil {
		return nil, errors.New(`config "` + file + `" is invalid: ` + err.Error())
	}
	s := &Service{tmp: filepath.Join(os.TempDir(), "twittoo"), users: make(map[string]account)}
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
	s.twit = &twitter.Client{
		Host: "https://api.twitter.com",
		Client: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: time.Second * 10, KeepAlive: time.Second * 30}).DialContext,
				MaxIdleConns:          256,
				IdleConnTimeout:       time.Second * 60,
				DisableKeepAlives:     false,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   time.Second * 10,
				ExpectContinueTimeout: time.Second * 10,
				ResponseHeaderTimeout: time.Second * 10,
			},
		},
		Authorizer: s,
	}
	if err = s.setupOauth(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret); err != nil {
		return nil, errors.New("twitter login failed: " + err.Error())
	}
	e := make([]string, 0, len(c.Users))
	for k := range c.Users {
		if len(k) == 0 { // Shouldn't happen
			continue
		}
		e = append(e, k)
	}
	r, err := s.twit.UserNameLookup(context.Background(), e, twitter.UserLookupOpts{UserFields: []twitter.UserField{twitter.UserFieldID, twitter.UserFieldUserName}})
	if err != nil {
		return nil, errors.New("twitter user lookup failed: " + err.Error())
	}
	for k, v := range c.Users {
		if len(k) == 0 {
			continue
		}
		var j string
		for i := range r.Raw.Users {
			if strings.EqualFold(k, r.Raw.Users[i].UserName) {
				j = r.Raw.Users[i].ID
				break
			}
		}
		if len(j) == 0 {
			return nil, errors.New(`twitter user "` + k + `" lookup did not return`)
		}
		s.log.Trace(`Resolved Twitter user "%s" to ID "%s".`, k, j)
		z, err := madon.RestoreApp("TwitToo", v.Server, v.Key, v.Secret, &madon.UserToken{AccessToken: v.Token, Scope: "write:media write:statuses"})
		if err != nil {
			return nil, errors.New(`mastodon login for "` + k + `" failed: "` + err.Error())
		}
		s.log.Trace(`Added Twitter/Mastodon mapping for user twitter:"%s" to mastodon:"%s".`, k, v.Server)
		s.users[j] = account{client: z, ignore: v.Ignore, private: v.UnList}
	}
	return s, nil
}
func (s *Service) setupOauth(k, p string) error {
	r, _ := http.NewRequest("POST", "https://api.twitter.com/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	r.SetBasicAuth(k, p)
	o, err := s.twit.Client.Do(r)
	if err != nil {
		return err
	}
	var i token
	err = json.NewDecoder(o.Body).Decode(&i)
	if o.Body.Close(); err != nil {
		return err
	}
	s.auth = i.Token
	return nil
}
