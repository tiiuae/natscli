// Copyright 2020 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/choria-io/fisk"
	"github.com/tiiuae/nats-server/v2/server"
)

type SrvMappingCmd struct {
	src  string
	dest string
	subj string
}

func configureServerMappingCommand(srv *fisk.CmdClause) {
	c := &SrvMappingCmd{}

	m := srv.Command("mappings", "Test subject mapping patterns").Alias("mapping").Action(c.mappingAction)
	m.Arg("source", "Source subject pattern").StringVar(&c.src)
	m.Arg("dest", "Destination subject pattern").StringVar(&c.dest)
	m.Arg("subject", "Subject to transform").StringVar(&c.subj)
}

func (c *SrvMappingCmd) mappingAction(_ *fisk.ParseContext) error {
	if c.src == "" {
		err := askOne(&survey.Input{
			Message: "Source subject pattern",
			Help:    "The pattern matching source subjects",
		}, &c.src, survey.WithValidator(survey.Required))
		if err != nil {
			return err
		}
	}

	if c.dest == "" {
		err := askOne(&survey.Input{
			Message: "Destination subject pattern",
			Help:    "The pattern matching describing the mapping to test",
		}, &c.dest, survey.WithValidator(survey.Required))
		if err != nil {
			return err
		}
	}

	trans, err := server.NewSubjectTransform(c.src, c.dest)
	if err != nil {
		return err
	}

	transAndShow := func(trans server.SubjectTransformer, subj string) {
		s, err := trans.Match(subj)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		}

		fmt.Println(s)
		fmt.Println()
	}

	if c.subj != "" {
		transAndShow(trans, c.subj)
		return nil
	}

	fmt.Println("Enter subjects to test, empty subject terminates.")
	fmt.Println()

	for {
		c.subj = ""
		err = askOne(&survey.Input{
			Message: "Subject",
			Help:    "Enter a subject that matching source and the mapping will be shown",
		}, &c.subj)
		if err != nil {
			return err
		}

		if c.subj == "" {
			break
		}

		transAndShow(trans, c.subj)
	}

	return nil
}
