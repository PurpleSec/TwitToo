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
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	madon "github.com/McKael/madon/v2"
	twitter "github.com/g8rswimmer/go-twitter/v2"
)

type token struct {
	_     [0]func()
	Type  string `json:"token_type"`
	Token string `json:"access_token"`
}
type mediaType struct {
	Alt  string
	Name string
}

func parseContentWarnings(s string) (string, bool, string) {
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
func parseTweetText(v *twitter.TweetObj, t *twitter.TweetRaw) string {
	s := html.UnescapeString(v.Text)
	if v.Entities == nil {
		return s
	}
	for i := range v.Entities.URLs {
		s = strings.ReplaceAll(s, v.Entities.URLs[i].URL, v.Entities.URLs[i].ExpandedURL)
	}
	for i := range v.Entities.Mentions {
		s = strings.ReplaceAll(s, v.Entities.Mentions[i].UserName, v.Entities.Mentions[i].UserName+"@twitter.com")
	}
	return s
}
func (s *Service) parseMedia(u account, t *twitter.TweetRaw) []int64 {
	var (
		v = t.Tweets[0]
		a = make(map[string]mediaType, 16)
	)
	for i := range v.Attachments.MediaKeys {
		for x := range t.Includes.Media {
			if v.Attachments.MediaKeys[i] != t.Includes.Media[x].Key {
				continue
			}
			if len(t.Includes.Media[x].URL) == 0 {
				continue
			}
			switch t.Includes.Media[x].Type {
			case "photo":
				a[t.Includes.Media[x].URL] = mediaType{Alt: t.Includes.Media[x].AltText, Name: "photo_" + strconv.Itoa(len(a)) + ".png"}
			case "video":
				a[t.Includes.Media[x].URL] = mediaType{Alt: t.Includes.Media[x].AltText, Name: "video_" + strconv.Itoa(len(a)) + ".mp4"}
			default:
			}
		}
	}
	if len(a) == 0 {
		return nil
	}
	s.log.Debug(`Tweet "twitter.com/%s/status/%s" has %d attachments.`, v.Source, v.ID, len(a))
	d := make([]int64, 0, len(a))
	for k, z := range a {
		r, err := s.twit.Client.Get(k)
		if err != nil {
			s.log.Error(`Could not get Tweet "twitter.com/%s/status/%s" media "%s": %s!`, v.Source, v.ID, k, err.Error())
			continue
		}
		e, err := u.client.UploadMediaReader(r.Body, z.Name, z.Alt, "")
		if r.Body.Close(); err != nil {
			s.log.Error(`Could not upload Tweet "twitter.com/%s/status/%s" media attachment "%s": %s!`, v.Source, v.ID, k, err.Error())
		}
		d = append(d, e.ID)
		s.log.Debug(`Created attachment ID %d from URL "%s"!`, e.ID, k)
	}
	return d
}
func isTweetSelfReply(v *twitter.TweetObj, t *twitter.TweetRaw) bool {
	if len(v.InReplyToUserID) == 0 || len(v.ReferencedTweets) == 0 {
		return true
	}
	if v.InReplyToUserID != v.AuthorID {
		return false
	}
	if len(t.Includes.Users) > 1 {
		return false
	}
	for i := range v.ReferencedTweets {
		if v.ReferencedTweets[i].Type != "replied_to" {
			return false
		}
		for x := range t.Includes.Tweets {
			if v.ReferencedTweets[i].ID != t.Includes.Tweets[x].ID {
				continue
			}
			if t.Includes.Tweets[x].AuthorID != v.AuthorID {
				return false
			}
			if len(t.Includes.Tweets[x].InReplyToUserID) == 0 || len(t.Includes.Tweets[x].ReferencedTweets) == 0 {
				continue
			}
			return false
		}
	}
	return true
}
func (s *Service) twitter(x context.Context, g *sync.WaitGroup, c chan<- *twitter.TweetRaw) {
	s.log.Info("Starting Twitter stream thread..")
	o, k, err := s.stream(x)
	if err != nil {
		s.log.Error("Error creating initial Twitter stream: %s!", err.Error())
		s.err = err
		s.cancel()
		return
	}
	r, m, e := o.Tweets(), o.SystemMessages(), o.DisconnectionError()
	for g.Add(1); ; {
		select {
		case <-e:
			s.log.Error("Twitter stream thread received a StreamDisconnect message!")
			s.log.Info("Waiting %s before retrying..", pause.String())
			time.Sleep(pause)
			if s.log.Info("Attempting to reload Twitter stream.."); len(k) > 0 {
				s.twit.TweetSearchStreamDeleteRuleByID(x, k, false)
			}
			o.Close()
			time.Sleep(time.Millisecond * 150)
			if o, k, s.err = s.stream(x); s.err != nil {
				s.log.Error("Error re-creating Twitter stream: %s!", s.err.Error())
				goto done
			}
			r, m, e = o.Tweets(), o.SystemMessages(), o.DisconnectionError()
		case o := <-m:
			if len(o) == 0 {
				break
			}
			for k, v := range o {
				s.log.Warning("Twitter stream thread received a %s message: %s!", k, v)
			}
		case n := <-r:
			if len(n.Raw.Tweets) == 0 {
				break
			}
			v := n.Raw.Tweets[0] // There isn't more than one Tweet in here mostly.
			if a := len(n.Raw.Tweets); a > 1 {
				s.log.Warning("Tweet container returned %d Tweets instead of just one!", a)
			}
			if len(n.Raw.Includes.Users) > 0 { // First user is usually the author.
				n.Raw.Tweets[0].Source = n.Raw.Includes.Users[0].UserName
			}
			s.log.Trace(
				`Tweet "%s" received! Details [Reply? %t, Retweet/Quote? %t, Size? %d, User? %s, URL? https://twitter.com/%s/status/%s]`,
				v.ID, (len(v.Text) > 0 && v.Text[0] == '@') || len(v.InReplyToUserID) > 0, len(v.ReferencedTweets) > 0, len(v.Text), v.Source,
				v.Source, v.ID,
			)
			if v.Text[0] == '@' && !isTweetSelfReply(v, n.Raw) || len(v.ReferencedTweets) > 0 {
				s.log.Debug(`Tweet "twitter.com/%s/status/%s" is a direct reply or retweet, skipping it!`, v.Source, v.ID)
				continue
			}
			c <- n.Raw
		case <-x.Done():
			s.log.Info("Stopping Twitter stream thread.")
			goto done
		}
	}
done:
	if o != nil {
		if len(k) > 0 {
			s.twit.TweetSearchStreamDeleteRuleByID(x, k, false)
		}
		o.Close()
	}
	s.log.Info("Stopped Twitter stream thread.")
	s.cancel()
	g.Done()
}
func (s *Service) mastodon(x context.Context, g *sync.WaitGroup, c <-chan *twitter.TweetRaw) {
	s.log.Info("Starting Mastodon sender thread..")
	for g.Add(1); ; {
		select {
		case t := <-c:
			if len(t.Tweets) == 0 {
				break
			}
			v := t.Tweets[0]
			s.log.Debug(`Received Tweet "twitter.com/%s/status/%s" to send!`, v.Source, v.ID)
			u, ok := s.users[v.AuthorID]
			if !ok {
				s.log.Warning(`Received a Tweet "twitter.com/%s/status/%s" without a matching user!`, v.Source, v.ID)
				break
			}
			s.log.Debug(`Running in the context of the user "%s" (%s).`, v.Source, v.ID)
			b, z, w, e := parseTweetText(v, t), v.PossiblySensitive, "", ""
			if len(u.private) > 0 && strings.HasPrefix(b, u.private) {
				b, e = strings.TrimSpace(b[len(u.private):]), "unlisted"
			}
			if !u.ignore {
				b, z, w = parseContentWarnings(b)
			}
			if x := strings.LastIndex(b, " https://twitter.com/"+v.Source+"/status/"); x > 0 {
				b = b[:x]
			}
			p, err := u.client.PostStatus(madon.PostStatusParams{
				Text:        b,
				MediaIDs:    s.parseMedia(u, t),
				Sensitive:   z,
				Visibility:  e,
				SpoilerText: w,
			})
			if err != nil {
				s.log.Error("Failed to post Mastodon status: %s!", err.Error())
				break
			}
			s.log.Info(`Posted Mastodon status %d "%s"!`, p.ID, p.URL)
		case <-x.Done():
			g.Done()
			return
		}
	}
}
func (s *Service) stream(x context.Context) (*twitter.TweetStream, []twitter.TweetSearchStreamRuleID, error) {
	u := make([]string, 0, len(s.users))
	for k := range s.users {
		u = append(u, "from:"+k)
	}
	s.log.Info("Twitter watch list generated, watching %d users.", len(u))
	var (
		r = make([]twitter.TweetSearchStreamRule, 0, 4)
		b strings.Builder
	)
	for i := range u {
		if len(u[i])+36+b.Len() >= 510 {
			r = append(r, twitter.TweetSearchStreamRule{Value: " (" + b.String() + ") -is:retweet -is:quote lang:en"})
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString(u[i])
	}
	if b.Len() > 0 {
		r = append(r, twitter.TweetSearchStreamRule{Value: "(" + b.String() + ") -is:retweet -is:quote lang:en"})
	}
	b.Reset()
	y, err := s.twit.TweetSearchStreamAddRule(x, r, false)
	if err != nil {
		return nil, nil, err
	}
	v := make([]twitter.TweetSearchStreamRuleID, len(y.Rules))
	for i := range y.Rules {
		v[i] = y.Rules[i].ID
	}
	o, err := s.twit.TweetSearchStream(x, twitter.TweetSearchStreamOpts{
		Expansions: []twitter.Expansion{
			twitter.ExpansionAuthorID,
			twitter.ExpansionReferencedTweetsID,
			twitter.ExpansionAttachmentsMediaKeys,
		},
		UserFields: []twitter.UserField{
			twitter.UserFieldID,
			twitter.UserFieldUserName,
		},
		MediaFields: []twitter.MediaField{
			twitter.MediaFieldURL,
			twitter.MediaFieldType,
			twitter.MediaFieldAltText,
			twitter.MediaFieldMediaKey,
		},
		TweetFields: []twitter.TweetField{
			twitter.TweetFieldID,
			twitter.TweetFieldText,
			twitter.TweetFieldAuthorID,
			twitter.TweetFieldEntities,
			twitter.TweetFieldAttachments,
			twitter.TweetFieldConversationID,
			twitter.TweetFieldInReplyToUserID,
			twitter.TweetFieldReferencedTweets,
			twitter.TweetFieldPossiblySensitve,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return o, v, err
}
