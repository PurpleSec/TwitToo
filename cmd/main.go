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

package main

import (
	"os"

	"github.com/PurpleSec/twittoo"
)

var version = "unknown"

func main() {
	if len(os.Args) != 2 {
		os.Stderr.WriteString(os.Args[0] + " [-V] <config_file>\n")
		os.Exit(2)
	}
	if os.Args[1] == "-V" {
		os.Stdout.WriteString("TwitToo: " + version + "\n")
		os.Exit(0)
	}
	s, err := twittoo.New(os.ExpandEnv(os.Args[1]))
	if err != nil {
		os.Stderr.WriteString(err.Error() + "!\n")
		os.Exit(1)
	}
	if err := s.Run(); err != nil {
		os.Stderr.WriteString(err.Error() + "!\n")
		os.Exit(1)
	}
}
