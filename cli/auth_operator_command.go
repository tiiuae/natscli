// Copyright 2023-2024 The NATS Authors
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
	"io"
	"net/url"
	"os"
	"sort"

	"github.com/AlecAivazis/survey/v2"
	"github.com/choria-io/fisk"
	ab "github.com/synadia-io/jwt-auth-builder.go"
)

type authOperatorCommand struct {
	operatorName         string
	operatorService      []*url.URL
	operatorServiceIsSet bool
	accountServer        *url.URL
	accountServerIsSet   bool
	listNames            bool
	force                bool
	createSK             bool
	tokenFile            string
	keyFiles             []string
	pubKey               string
	outputFile           string
}

func configureAuthOperatorCommand(auth commandHost) {
	c := &authOperatorCommand{}

	op := auth.Command("operator", "Manage NATS Operators").Alias("o").Alias("op")

	add := op.Command("add", "Adds a new Operator").Action(c.addAction)
	add.Arg("name", "Unique name for this Operator").StringVar(&c.operatorName)
	add.Flag("service", "URLs for the Operator services").PlaceHolder("URL").URLListVar(&c.operatorService)
	add.Flag("account-server", "URL for the account server").PlaceHolder("URL").URLVar(&c.accountServer)
	add.Flag("signing-key", "Creates a signing key for this account").Default("true").BoolVar(&c.createSK)

	info := op.Command("info", "Show Operator information").Alias("i").Alias("show").Alias("view").Action(c.infoAction)
	info.Arg("name", "Operator to view").StringVar(&c.operatorName)

	ls := op.Command("list", "List Operators").Alias("ls").Action(c.lsAction)
	ls.Flag("names", "Show just the Operator names").UnNegatableBoolVar(&c.listNames)

	edit := op.Command("edit", "Edit an Operator").Alias("update").Action(c.editAction)
	edit.Arg("name", "Operator to edit").StringVar(&c.operatorName)
	edit.Flag("account-server", "URL for the Account Server").IsSetByUser(&c.accountServerIsSet).PlaceHolder("URL").URLVar(&c.accountServer)
	edit.Flag("service", "URLs for the Operator Services").IsSetByUser(&c.operatorServiceIsSet).PlaceHolder("URL").URLListVar(&c.operatorService)

	imp := op.Command("import", "Imports an operator").Action(c.importAction)
	imp.Arg("token", "The JWT file containing the account to import").Required().PlaceHolder("JWT").ExistingFileVar(&c.tokenFile)
	imp.Arg("key", "List of keys to import").PlaceHolder("FILE").ExistingFilesVar(&c.keyFiles)

	gen := op.Command("generate", "Generates a memory resolver configuration").Alias("gen").Action(c.generateAction)
	gen.Arg("name", "Operator to act on").StringVar(&c.operatorName)
	gen.Flag("output", "Write resolver to a file").PlaceHolder("FILE").Short('o').StringVar(&c.outputFile)
	gen.Flag("force", "Overwrite existing files without prompting").Short('f').UnNegatableBoolVar(&c.force)

	sk := op.Command("keys", "Manage Operator Signing Keys").Alias("sk").Alias("s")

	skls := sk.Command("list", "List Signing Keys").Alias("ls").Action(c.skListAction)
	skls.Arg("name", "Operator to act on").StringVar(&c.operatorName)

	skadd := sk.Command("add", "Adds a new Signing Key").Alias("new").Alias("create").Action(c.skAddAction)
	skadd.Arg("name", "Operator to act on").StringVar(&c.operatorName)

	skrm := sk.Command("rm", "Removes a Signing Key").Alias("delete").Action(c.skRmAction)
	skrm.Arg("name", "Operator to act on").StringVar(&c.operatorName)
	skrm.Arg("key", "The public key to remove").StringVar(&c.pubKey)
	skrm.Flag("force", "Remove without prompting").Short('f').UnNegatableBoolVar(&c.force)
}

func (c *authOperatorCommand) selectOperator(pick bool) (*ab.AuthImpl, ab.Operator, error) {
	auth, oper, err := selectOperator(c.operatorName, pick)
	if err != nil {
		return nil, nil, err
	}

	c.operatorName = oper.Name()

	return auth, oper, err
}

func (c *authOperatorCommand) skRmAction(_ *fisk.ParseContext) error {
	if c.pubKey == "" {
		return fmt.Errorf("public key is required")
	}

	auth, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	if !c.force {
		ok, err := askConfirmation(fmt.Sprintf("Really remove the signing key %s", c.pubKey), false)
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}
	}

	ok, err := operator.SigningKeys().Delete(c.pubKey)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("signing key was not found")
	}

	err = auth.Commit()
	if err != nil {
		return err
	}

	fmt.Println("Signing key removed")

	return nil
}

func (c *authOperatorCommand) skAddAction(_ *fisk.ParseContext) error {
	auth, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	k, err := operator.SigningKeys().Add()
	if err != nil {
		return err
	}

	err = auth.Commit()
	if err != nil {
		return err
	}

	fmt.Println(k)

	return nil
}

func (c *authOperatorCommand) skListAction(_ *fisk.ParseContext) error {
	_, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	for _, k := range operator.SigningKeys().List() {
		fmt.Println(k)
	}

	return nil
}

func (c *authOperatorCommand) generateAction(_ *fisk.ParseContext) error {
	_, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	out, err := operator.MemResolver()
	if err != nil {
		return err
	}

	if c.outputFile == "" {
		fmt.Println(string(out))

		return nil
	}

	if !c.force && fileExists(c.outputFile) {
		ok, err := askConfirmation(fmt.Sprintf("Overwrite %s", c.outputFile), false)
		if err != nil {
			return err
		}

		if !ok {
			return fmt.Errorf("%s exists", c.outputFile)
		}
	}

	os.Remove(c.outputFile)

	return os.WriteFile(c.outputFile, out, 0600)
}

func (c *authOperatorCommand) importAction(_ *fisk.ParseContext) error {
	auth, err := getAuthBuilder()
	if err != nil {
		return err
	}

	var token []byte
	var keys []string

	token, err = os.ReadFile(c.tokenFile)
	if err != nil {
		return err
	}

	for _, f := range c.keyFiles {
		key, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		keys = append(keys, string(key))
	}

	op, err := auth.Operators().Import(token, keys)
	if err != nil {
		return err
	}

	err = auth.Commit()
	if err != nil {
		return err
	}

	return c.fShowOperator(os.Stdout, op)
}

func (c *authOperatorCommand) fShowOperator(w io.Writer, op ab.Operator) error {
	out, err := c.showOperator(op)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, out)

	return err
}

func (c *authOperatorCommand) editAction(_ *fisk.ParseContext) error {
	auth, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	if c.accountServerIsSet {
		u := ""
		if c.accountServer != nil {
			u = c.accountServer.String()
		}

		err = operator.SetAccountServerURL(u)
		if err != nil {
			return err
		}
	}

	if c.operatorServiceIsSet {
		list := []string{}
		if c.operatorService != nil {
			for _, s := range c.operatorService {
				list = append(list, s.String())
			}
		}

		err = operator.SetOperatorServiceURL(list...)
		if err != nil {
			return err
		}
	}

	err = auth.Commit()
	if err != nil {
		return err
	}

	return c.fShowOperator(os.Stdout, operator)
}

func (c *authOperatorCommand) infoAction(_ *fisk.ParseContext) error {
	_, operator, err := c.selectOperator(true)
	if err != nil {
		return err
	}

	return c.fShowOperator(os.Stdout, operator)
}

func (c *authOperatorCommand) lsAction(_ *fisk.ParseContext) error {
	auth, err := getAuthBuilder()
	if err != nil {
		return err
	}

	list := auth.Operators().List()
	if len(list) == 0 {
		fmt.Println("No Operators found")
		return nil
	}

	if c.listNames {
		for _, op := range list {
			fmt.Println(op.Name())
		}
		return nil
	}

	table := newTableWriter("Operators")
	table.AddHeaders("Name", "Subject", "Accounts", "Account Server", "Signing Keys")
	for _, op := range list {
		table.AddRow(op.Name(), op.Subject(), len(op.Accounts().List()), op.AccountServerURL(), len(op.SigningKeys().List()))
	}
	fmt.Println(table.Render())

	return nil
}

func (c *authOperatorCommand) addAction(_ *fisk.ParseContext) error {
	if c.operatorName == "" {
		err := askOne(&survey.Input{
			Message: "Operator Name",
			Help:    "A unique name for the Operator being added",
		}, &c.operatorName, survey.WithValidator(survey.Required))
		if err != nil {
			return err
		}
	}

	auth, err := getAuthBuilder()
	if err != nil {
		return err
	}

	if isAuthItemKnown(auth.Operators().List(), c.operatorName) {
		return fmt.Errorf("operator %s already exist", c.operatorName)
	}

	operator, err := auth.Operators().Add(c.operatorName)
	if err != nil {
		return err
	}

	if c.operatorService != nil {
		list := []string{}
		for _, s := range c.operatorService {
			list = append(list, s.String())
		}

		err = operator.SetOperatorServiceURL(list...)
		if err != nil {
			return err
		}
	}

	if c.accountServer != nil {
		err = operator.SetAccountServerURL(c.accountServer.String())
		if err != nil {
			return err
		}
	}

	// always creating a system account for new operators
	system, err := operator.Accounts().Add("SYSTEM")
	if err != nil {
		return err
	}

	err = operator.SetSystemAccount(system)
	if err != nil {
		return err
	}

	if c.createSK {
		_, err = operator.SigningKeys().Add()
		if err != nil {
			return err
		}
	}

	err = auth.Commit()
	if err != nil {
		return err
	}

	operator, err = auth.Operators().Get(c.operatorName)
	if err != nil {
		return err
	}

	return c.fShowOperator(os.Stdout, operator)
}

func (c *authOperatorCommand) showOperator(operator ab.Operator) (string, error) {
	cols := newColumns("Operator %s (%s)", operator.Name(), operator.Subject())
	cols.AddSectionTitle("Configuration")
	cols.AddRow("Name", operator.Name())
	cols.AddRow("Subject", operator.Subject())
	cols.AddRowIf("Service URL(s)", operator.OperatorServiceURLs(), len(operator.OperatorServiceURLs()) > 0)
	cols.AddRowIfNotEmpty("Account Server", operator.AccountServerURL())
	cols.AddRow("Accounts", len(operator.Accounts().List()))

	sa, err := operator.SystemAccount()
	if err == nil {
		cols.AddRowf("System Account", "%s (%s)", sa.Name(), sa.Subject())
	} else {
		cols.AddRow("System Account", "not set")
	}

	if len(operator.SigningKeys().List()) > 0 {
		list := []string{}
		list = append(list, operator.SigningKeys().List()...)
		sort.Strings(list)

		cols.AddStringsAsValue("Signing Keys", list)
	}

	return cols.Render()
}
