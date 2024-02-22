// Copyright 2024 The NATS Authors
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
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/choria-io/fisk"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/tiiuae/nats.go"
	terminal "golang.org/x/term"
)

type SrvWatchAccountCmd struct {
	topCount  int
	sort      string
	accounts  map[string]map[string]server.AccountNumConns
	sortNames map[string]string
	lastMsg   time.Time
	mu        sync.Mutex
}

func configureServerWatchAccountCommand(watch *fisk.CmdClause) {
	c := &SrvWatchAccountCmd{
		accounts: map[string]map[string]server.AccountNumConns{},
		sortNames: map[string]string{
			"conns": "Connections",
			"subs":  "Subscriptions",
			"sentb": "Sent Bytes",
			"sentm": "Sent Messages",
			"recvb": "Received Bytes",
			"recvm": "Received Messages",
			"slow":  "Slow Consumers",
		},
	}
	sortKeys := mapKeys(c.sortNames)
	sort.Strings(sortKeys)

	accounts := watch.Command("accounts", "Watch account usage").Alias("account").Alias("acct").Action(c.accountsAction)
	accounts.HelpLong(`This waits for regular updates that each server sends and report seen totals.

Since the updates are sent on a 30 second interval this is not a point in time view.
The 'Servers' column will show how many servers sent statistics about an account.
Only servers with active connections will send these updates.
`)
	accounts.Flag("sort", fmt.Sprintf("Sorts by a specific property (%s)", strings.Join(sortKeys, ", "))).Default("conns").EnumVar(&c.sort, sortKeys...)
	accounts.Flag("number", "Amount of Accounts to show by the selected dimension").Default("0").Short('n').IntVar(&c.topCount)
}

func (c *SrvWatchAccountCmd) accountsAction(_ *fisk.ParseContext) error {
	_, h, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err != nil && c.topCount == 0 {
		return fmt.Errorf("could not determine screen dimensions: %v", err)
	}

	if c.topCount == 0 {
		c.topCount = h - 8
	}

	if c.topCount < 1 {
		return fmt.Errorf("requested render limits exceed screen size")
	}

	if c.topCount > h-8 {
		c.topCount = h - 8
	}

	nc, _, err := prepareHelper("", natsOpts()...)
	if err != nil {
		return err
	}

	_, err = nc.Subscribe("$SYS.ACCOUNT.*.SERVER.CONNS", c.handle)
	if err != nil {
		return err
	}

	tick := time.NewTicker(time.Second)
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	for {
		select {
		case <-tick.C:
			c.redraw()
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *SrvWatchAccountCmd) handle(msg *nats.Msg) {
	var conns server.AccountNumConns
	err := json.Unmarshal(msg.Data, &conns)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.accounts[conns.Account]
	if !ok {
		c.accounts[conns.Account] = map[string]server.AccountNumConns{}
	}
	c.accounts[conns.Account][conns.Server.ID] = conns
	c.lastMsg = time.Now()
}

func (c *SrvWatchAccountCmd) redraw() {
	c.mu.Lock()
	defer c.mu.Unlock()

	var accounts []*server.AccountStat
	seen := map[string]int{}

	for acct := range c.accounts {
		srvs, total := c.accountTotal(acct)
		seen[acct] = srvs
		accounts = append(accounts, total)
	}

	sort.Slice(accounts, func(i, j int) bool {
		ai := accounts[i]
		aj := accounts[j]

		switch c.sort {
		case "subs":
			return sortMultiSort(ai.NumSubs, aj.NumSubs, ai.Conns, aj.Conns)
		case "slow":
			return sortMultiSort(ai.SlowConsumers, aj.SlowConsumers, ai.Conns, aj.Conns)
		case "sentb":
			return sortMultiSort(ai.Sent.Bytes, aj.Sent.Bytes, ai.Conns, aj.Conns)
		case "sentm":
			return sortMultiSort(ai.Sent.Msgs, aj.Sent.Msgs, ai.Conns, aj.Conns)
		case "recvb":
			return sortMultiSort(ai.Received.Bytes, aj.Received.Bytes, ai.Conns, aj.Conns)
		case "recvm":
			return sortMultiSort(ai.Received.Msgs, aj.Received.Msgs, ai.Conns, aj.Conns)
		default:
			return sortMultiSort(ai.Conns, aj.Conns, ai.Conns, aj.Conns)
		}
	})

	tc := fmt.Sprintf("%d", len(accounts))
	if len(accounts) > c.topCount {
		tc = fmt.Sprintf("%d / %d", c.topCount, len(accounts))
	}

	table := newTableWriter(fmt.Sprintf("Top %s Account activity by %s at %s", tc, c.sortNames[c.sort], c.lastMsg.Format(time.DateTime)))
	table.AddHeaders("Account", "Servers", "Connections", "Leafnodes", "Subscriptions", "Slow", "Sent", "Received")

	var matched []*server.AccountStat
	if len(accounts) < c.topCount {
		matched = accounts
	} else {
		matched = accounts[:c.topCount]
	}

	for _, account := range matched {
		table.AddRow(
			account.Account,
			seen[account.Account],
			f(account.Conns),
			f(account.LeafNodes),
			f(account.NumSubs),
			f(account.SlowConsumers),
			fmt.Sprintf("%s / %s", f(account.Sent.Msgs), fiBytes(uint64(account.Sent.Bytes))),
			fmt.Sprintf("%s / %s", f(account.Received.Msgs), fiBytes(uint64(account.Received.Bytes))),
		)
	}

	clearScreen()
	fmt.Println(table.Render())
}

func (c *SrvWatchAccountCmd) accountTotal(acct string) (int, *server.AccountStat) {
	stats, ok := c.accounts[acct]
	if !ok {
		return 0, nil
	}

	total := &server.AccountStat{}
	var servers int

	for _, stat := range stats {
		// ignore old data since only servers with connections will send these
		// we could lose a connection and the server should send one update but
		// if we miss it we might have stale stuff here, so just ignore old things
		if time.Since(stat.Server.Time) > 35*time.Second {
			continue
		}
		servers++

		total.Account = acct
		total.TotalConns += stat.TotalConns
		total.Conns += stat.Conns
		total.LeafNodes += stat.LeafNodes
		total.NumSubs += stat.NumSubs
		total.SlowConsumers += stat.SlowConsumers
		total.Sent.Bytes += stat.Sent.Bytes
		total.Sent.Msgs += stat.Sent.Msgs
		total.Received.Msgs += stat.Received.Msgs
		total.Received.Bytes += stat.Received.Bytes
	}

	return servers, total
}
