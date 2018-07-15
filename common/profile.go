/*

Copyright (C) 2017-2018  Ettore Di Giacinto <mudler@gentoo.org>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

*/

package common

import (
	"errors"
)

const (
	MCLI_ENV_PREFIX  = "MOTTAINAI_CLI"
	MCLI_CONFIG_NAME = "mcli-profiles"
	// NOTE: doesn't use $HOME because os.Mkdir doesn't resolve it.
	MCLI_HOME_PATH  = ".config/mottainai"
	MCLI_LOCAL_PATH = ".mottainai"
)

// NOTE: For viper unmarshal it is needed that
//       object have public attribute

type Profile struct {
	Master string `mapstructure:"master"`
}

type ProfileConf struct {
	Profiles map[string](Profile) `mapstructure:"profiles"`
}

func NewProfileConf() *ProfileConf {
	return &ProfileConf{Profiles: make(map[string]Profile)}
}

func (p *ProfileConf) GetProfile(name string) (*Profile, error) {
	var ans *Profile = nil

	if name == "" {
		return nil, errors.New("Invalid name")
	}

	profile, ok := p.Profiles[name]

	if ok {
		ans = &profile
	}

	return ans, nil
}

func (p *ProfileConf) AddProfile(name string, master string) error {

	if name == "" {
		return errors.New("Invalid name")
	}

	if master == "" {
		return errors.New("Invalid master url")
	}

	// If all profiles are removed then Profiles is nil
	if p.Profiles == nil {
		p.Profiles = make(map[string]Profile)
	}
	p.Profiles[name] = Profile{Master: master}

	return nil
}

func (p *ProfileConf) RemoveProfile(name string) *Profile {
	var ans *Profile

	profile, ok := p.Profiles[name]
	if ok {
		ans = &profile
		delete(p.Profiles, name)
	}

	return ans
}

func (p *Profile) GetMaster() string {
	return p.Master
}
