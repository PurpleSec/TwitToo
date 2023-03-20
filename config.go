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

import "errors"

type config struct {
	Twitter struct {
		ConsumerKey    string `json:"consumer_key"`
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

func (c config) verify() error {
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
	if len(c.Twitter.ConsumerKey) == 0 {
		return errors.New(`"twitter.consumer_key" cannot be empty`)
	}
	if len(c.Twitter.ConsumerSecret) == 0 {
		return errors.New(`"twitter.consumer_secret" cannot be empty`)
	}
	return nil
}
