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
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/choria-io/fisk"
	"github.com/dustin/go-humanize"
	"github.com/fatih/color"
	"github.com/gosuri/uiprogress"
	"github.com/nats-io/jsm.go"
	"github.com/tiiuae/nats.go"
)

type objCommand struct {
	bucket              string
	file                string
	overrideName        string
	hdrs                []string
	force               bool
	progress            bool
	storage             string
	listNames           bool
	placementCluster    string
	placementTags       []string
	maxBucketSize       int64
	maxBucketSizeString string
	metadata            map[string]string

	description string
	replicas    uint
	ttl         time.Duration
	compression bool
}

func configureObjectCommand(app commandHost) {
	c := &objCommand{
		metadata: map[string]string{},
	}

	help := `Interacts with a JetStream based Object store

The JetStream Object store uses streams to store large objects
for an indefinite period or a per-bucket configured TTL.

NOTE: This is an experimental feature.
`

	obj := app.Command("object", help).Alias("obj")
	addCheat("obj", obj)

	add := obj.Command("add", "Adds a new Object Store Bucket").Action(c.addAction)
	add.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
	add.Flag("ttl", "How long to keep objects for").DurationVar(&c.ttl)
	add.Flag("replicas", "How many replicas of the data to store").Default("1").UintVar(&c.replicas)
	add.Flag("max-bucket-size", "Maximum size for the bucket").StringVar(&c.maxBucketSizeString)
	add.Flag("description", "A description for the bucket").StringVar(&c.description)
	add.Flag("storage", "Storage backend to use (file, memory)").EnumVar(&c.storage, "file", "f", "memory", "m")
	add.Flag("tags", "Place the store on servers that has specific tags").StringsVar(&c.placementTags)
	add.Flag("cluster", "Place the store on a specific cluster").StringVar(&c.placementCluster)
	add.Flag("metadata", "Adds metadata to the bucvket").PlaceHolder("META").StringMapVar(&c.metadata)
	add.Flag("compress", "Compress the bucket data").BoolVar(&c.compression)

	add.PreAction(c.parseLimitStrings)

	put := obj.Command("put", "Puts a file into the store").Action(c.putAction)
	put.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
	put.Arg("file", "The file to put").ExistingFileVar(&c.file)
	put.Flag("name", "Override the name supplied to the object store").StringVar(&c.overrideName)
	put.Flag("description", "Sets an optional description for the object").StringVar(&c.description)
	put.Flag("header", "Adds headers to the object").Short('H').StringsVar(&c.hdrs)
	put.Flag("progress", "Disable progress bars").Default("true").BoolVar(&c.progress)
	put.Flag("force", "Act without confirmation").Short('f').UnNegatableBoolVar(&c.force)

	del := obj.Command("del", "Deletes a file or bucket from the store").Action(c.delAction).Alias("rm")
	del.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
	del.Arg("file", "The file to retrieve").StringVar(&c.file)
	del.Flag("force", "Act without confirmation").Short('f').UnNegatableBoolVar(&c.force)

	get := obj.Command("get", "Retrieves a file from the store").Action(c.getAction)
	get.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
	get.Arg("file", "The file to retrieve").Required().StringVar(&c.file)
	get.Flag("output", "Override the output file name").Short('O').StringVar(&c.overrideName)
	get.Flag("progress", "Disable progress bars").Default("true").BoolVar(&c.progress)
	get.Flag("force", "Act without confirmation").Short('f').UnNegatableBoolVar(&c.force)

	info := obj.Command("info", "Get information about a bucket or object").Alias("show").Alias("i").Action(c.infoAction)
	info.Arg("bucket", "The bucket to act on").StringVar(&c.bucket)
	info.Arg("file", "The file to retrieve").StringVar(&c.file)

	ls := obj.Command("ls", "List buckets or contents of a specific bucket").Action(c.lsAction)
	ls.Arg("bucket", "The bucket to act on").StringVar(&c.bucket)
	ls.Flag("names", "When listing buckets, show just the bucket names").Short('n').UnNegatableBoolVar(&c.listNames)

	seal := obj.Command("seal", "Seals a bucket preventing further updates").Action(c.sealAction)
	seal.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
	seal.Flag("force", "Force sealing without prompting").Short('f').UnNegatableBoolVar(&c.force)

	watch := obj.Command("watch", "Watch a bucket for changes").Action(c.watchAction)
	watch.Arg("bucket", "The bucket to act on").Required().StringVar(&c.bucket)
}

func init() {
	registerCommand("object", 10, configureObjectCommand)
}

func (c *objCommand) parseLimitStrings(_ *fisk.ParseContext) (err error) {
	if c.maxBucketSizeString != "" {
		c.maxBucketSize, err = parseStringAsBytes(c.maxBucketSizeString)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *objCommand) watchAction(_ *fisk.ParseContext) error {
	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	w, err := obj.Watch(nats.IncludeHistory())
	if err != nil {
		return err
	}
	defer w.Stop()

	for i := range w.Updates() {
		if i == nil {
			continue
		}

		if i.Deleted {
			fmt.Printf("[%s] %s %s > %s\n", f(i.ModTime), color.RedString("DEL"), i.Bucket, i.Name)
		} else {
			fmt.Printf("[%s] %s %s > %s: %s bytes in %s chunks\n", f(i.ModTime), color.GreenString("PUT"), i.Bucket, i.Name, humanize.IBytes(i.Size), f(i.Chunks))
		}
	}

	return nil
}

func (c *objCommand) sealAction(_ *fisk.ParseContext) error {
	if !c.force {
		ok, err := askConfirmation(fmt.Sprintf("Really seal Bucket %s, sealed buckets can not be unsealed or modified", c.bucket), false)
		fisk.FatalIfError(err, "could not obtain confirmation")

		if !ok {
			return nil
		}
	}

	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	err = obj.Seal()
	if err != nil {
		return err
	}

	fmt.Printf("%s has been sealed\n", c.bucket)

	return c.showBucketInfo(obj)
}

func (c *objCommand) delAction(_ *fisk.ParseContext) error {
	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	if c.file != "" {
		if !c.force {
			nfo, err := obj.GetInfo(c.file)
			if err != nil {
				return err
			}

			ok, err := askConfirmation(fmt.Sprintf("Delete %s byte file %s > %s?", humanize.IBytes(nfo.Size), c.bucket, c.file), false)
			if err != nil {
				return err
			}

			if !ok {
				fmt.Println("Skipping delete")
				return nil
			}
		}
		err = obj.Delete(c.file)
		if err != nil {
			return err
		}

		fmt.Printf("Removed %s > %s\n", c.bucket, c.file)

		return c.showBucketInfo(obj)

	} else {
		if !c.force {
			ok, err := askConfirmation(fmt.Sprintf("Delete bucket %s and all its files?", c.bucket), false)
			if err != nil {
				return err
			}

			if !ok {
				fmt.Println("Skipping delete")
				return nil
			}
		}

		_, js, err := prepareJSHelper()
		if err != nil {
			return err
		}

		return js.DeleteObjectStore(c.bucket)
	}
}

func (c *objCommand) infoAction(_ *fisk.ParseContext) error {
	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	if c.file == "" {
		return c.showBucketInfo(obj)
	}

	nfo, err := obj.GetInfo(c.file)
	if err != nil {
		return err
	}

	c.showObjectInfo(nfo)

	return nil
}

func (c *objCommand) showBucketInfo(store nats.ObjectStore) error {
	status, err := store.Status()
	if err != nil {
		return err
	}

	var nfo *nats.StreamInfo
	if status.BackingStore() == "JetStream" {
		nfo = status.(*nats.ObjectBucketStatus).StreamInfo()
	}

	cols := newColumns("")
	defer cols.Frender(os.Stdout)

	if nfo == nil {
		cols.SetHeading(fmt.Sprintf("Information for Object Store Bucket %s", status.Bucket()))
	} else {
		cols.SetHeading(fmt.Sprintf("Information for Object Store Bucket %s created %s", status.Bucket(), nfo.Created.Local().Format(time.RFC3339)))
	}

	cols.AddSectionTitle("Configuration")
	cols.AddRowf("Bucket Name", status.Bucket())
	cols.AddRowIfNotEmpty("Description", status.Description())
	cols.AddRow("Replicas", status.Replicas())
	if status.TTL() == 0 {
		cols.AddRow("TTL", "unlimited")
	} else {
		cols.AddRow("TTL", status.TTL())
	}
	cols.AddRow("Sealed", status.Sealed())
	cols.AddRow("Size", humanize.IBytes(status.Size()))
	if nfo != nil {
		if nfo.Config.MaxBytes == -1 {
			cols.AddRow("Maximum Bucket Size", "unlimited")
		} else {
			cols.AddRow("Maximum Bucket Size", humanize.IBytes(uint64(nfo.Config.MaxBytes)))
		}
	}
	cols.AddRow("Backing Store Kind", status.BackingStore())
	if status.BackingStore() == "JetStream" {
		cols.AddRow("JetStream Stream", nfo.Config.Name)

		if len(nfo.Config.Metadata) > 0 {
			cols.AddMapStringsAsValue("Metadata", nfo.Config.Metadata)
		}

		if nfo.Cluster != nil {
			cols.AddSectionTitle("Cluster Information")
			renderNatsGoClusterInfo(cols, nfo)
		}
	}

	return nil
}

func (c *objCommand) showObjectInfo(nfo *nats.ObjectInfo) {
	digest := strings.Split(nfo.Digest, "=")
	digestBytes, _ := base64.URLEncoding.DecodeString(digest[1])

	fmt.Printf("Object information for %s > %s\n\n", nfo.Bucket, nfo.Name)
	if nfo.Description != "" {
		fmt.Printf("      Description: %s\n", nfo.Description)
	}
	fmt.Printf("               Size: %s\n", humanize.IBytes(nfo.Size))
	fmt.Printf("  Modification Time: %s\n", nfo.ModTime.Format(time.RFC822Z))
	fmt.Printf("             Chunks: %s\n", f(nfo.Chunks))
	fmt.Printf("             Digest: %s %x\n", digest[0], digestBytes)
	if nfo.Deleted {
		fmt.Printf("            Deleted: %v\n", nfo.Deleted)
	}
	if len(nfo.Headers) > 0 {
		fmt.Printf("            Headers: ")
		first := true
		for k, v := range nfo.Headers {
			for _, i := range v {
				if first {
					fmt.Printf("%s: %s\n", k, i)
					first = false
				} else {
					fmt.Printf("                     %s: %s\n", k, i)
				}
			}
		}
	}
}

func (c *objCommand) listBuckets() error {
	_, mgr, err := prepareHelper("", natsOpts()...)
	if err != nil {
		return err
	}

	var found []*jsm.Stream
	_, err = mgr.EachStream(nil, func(s *jsm.Stream) {
		if s.IsObjectBucket() {
			found = append(found, s)
		}
	})
	if err != nil {
		return err
	}

	if len(found) == 0 {
		fmt.Println("No Object Store buckets found")
		return nil
	}

	if c.listNames {
		for _, s := range found {
			fmt.Println(strings.TrimPrefix(s.Name(), "OBJ_"))
		}
		return nil
	}

	sort.Slice(found, func(i, j int) bool {
		info, _ := found[i].LatestInformation()
		jnfo, _ := found[j].LatestInformation()

		return info.State.Bytes < jnfo.State.Bytes
	})

	table := newTableWriter("Object Store Buckets")
	table.AddHeaders("Bucket", "Description", "Created", "Size", "Last Update")
	for _, s := range found {
		nfo, _ := s.LatestInformation()

		table.AddRow(strings.TrimPrefix(s.Name(), "OBJ_"), s.Description(), f(nfo.Created), humanize.IBytes(nfo.State.Bytes), f(time.Since(nfo.State.LastTime)))
	}

	fmt.Println(table.Render())

	return nil
}

func (c *objCommand) lsAction(_ *fisk.ParseContext) error {
	if c.bucket == "" {
		return c.listBuckets()
	}

	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	contents, err := obj.List()
	if err != nil {
		return err
	}

	if len(contents) == 0 {
		fmt.Println("No entries found")
		return nil
	}

	if c.listNames {
		for _, s := range contents {
			fmt.Println(s.Name)
		}
		return nil
	}

	table := newTableWriter("Bucket Contents")
	table.AddHeaders("Name", "Size", "Time")

	for _, i := range contents {
		table.AddRow(i.Name, humanize.IBytes(i.Size), i.ModTime.Format(time.RFC3339))
	}

	fmt.Println(table.Render())

	return nil
}

func (c *objCommand) putAction(_ *fisk.ParseContext) error {
	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	name := c.file
	if c.overrideName != "" {
		name = c.overrideName
	}

	if c.file == "" && name == "" {
		return fmt.Errorf("--name is required when reading from stdin")
	}

	nfo, err := obj.GetInfo(name)
	if err == nil && !nfo.Deleted && !c.force {
		c.showObjectInfo(nfo)
		fmt.Println()
		ok, err := askConfirmation(fmt.Sprintf("Replace existing file %s > %s", c.bucket, name), false)
		fisk.FatalIfError(err, "could not obtain confirmation")

		if !ok {
			return nil
		}
	}

	hdr, err := parseStringsToHeader(c.hdrs, 0)
	if err != nil {
		return err
	}

	var (
		pr   io.Reader
		stat os.FileInfo
	)

	if c.file == "" {
		pr = os.Stdin
	} else {
		f, err := os.Open(c.file)
		if err != nil {
			return err
		}
		defer f.Close()

		stat, err = f.Stat()
		if err != nil {
			return err
		}

		pr = f
	}

	meta := &nats.ObjectMeta{
		Name:        filepath.Clean(name),
		Description: c.description,
		Headers:     hdr,
	}

	var progress *uiprogress.Bar
	stop := func() {}

	if !opts.Trace && c.progress && stat != nil && stat.Size() > 20480 {
		hs := humanize.IBytes(uint64(stat.Size()))
		progress = uiprogress.AddBar(int(stat.Size())).PrependFunc(func(b *uiprogress.Bar) string {
			return fmt.Sprintf("%s / %s", humanize.IBytes(uint64(b.Current())), hs)
		})
		progress.Width = progressWidth()

		fmt.Println()
		uiprogress.Start()
		stop = func() { uiprogress.Stop(); fmt.Println() }
		pr = &progressRW{p: progress, r: pr}
	}

	nfo, err = obj.Put(meta, pr)
	stop()
	if err != nil {
		return err
	}

	c.showObjectInfo(nfo)

	return nil
}

func (c *objCommand) getAction(_ *fisk.ParseContext) error {
	_, _, obj, err := c.loadBucket()
	if err != nil {
		return err
	}

	res, err := obj.Get(c.file)
	if err != nil {
		return err
	}

	nfo, err := res.Info()
	if err != nil {
		return err
	}

	if nfo.Deleted {
		return fmt.Errorf("file has been deleted")
	}

	out := filepath.Base(nfo.Name)
	if c.overrideName != "" {
		out = c.overrideName
	}

	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}

	if !c.force {
		_, err = os.Stat(out)
		if !os.IsNotExist(err) {
			ok, err := askConfirmation(fmt.Sprintf("Replace existing target file %s", out), false)
			fisk.FatalIfError(err, "could not obtain confirmation")

			if !ok {
				return nil
			}
		}
	}

	of, err := os.Create(out)
	if err != nil {
		return err
	}
	defer of.Close()

	var progress *uiprogress.Bar
	pw := io.Writer(of)
	stop := func() {}

	if !opts.Trace && c.progress && nfo.Size > 20480 {
		hs := humanize.IBytes(nfo.Size)
		progress = uiprogress.AddBar(int(nfo.Size)).PrependFunc(func(b *uiprogress.Bar) string {
			return fmt.Sprintf("%s / %s", humanize.IBytes(uint64(b.Current())), hs)
		})
		progress.Width = progressWidth()

		fmt.Println()
		uiprogress.Start()
		stop = func() { uiprogress.Stop(); fmt.Println() }
		pw = &progressRW{p: progress, w: of}
	}

	start := time.Now()
	wc, err := io.Copy(pw, res)
	stop()
	if err != nil {
		of.Close()
		os.Remove(of.Name())
		return err
	}

	if wc > 0 && uint64(wc) != nfo.Size {
		return fmt.Errorf("wrote %s, expected %s", humanize.IBytes(uint64(wc)), humanize.IBytes(nfo.Size))
	}

	of.Close()

	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		bps := float64(nfo.Size) / elapsed.Seconds()
		fmt.Printf("Wrote: %s to %s in %v average %s/s\n", humanize.IBytes(uint64(wc)), of.Name(), f(elapsed), humanize.IBytes(uint64(bps)))
	} else {
		fmt.Printf("Wrote: %s to %s in %v\n", humanize.IBytes(uint64(wc)), of.Name(), f(elapsed))
	}

	return nil
}

func (c *objCommand) addAction(_ *fisk.ParseContext) error {
	_, js, err := prepareJSHelper()
	if err != nil {
		return err
	}

	st := nats.FileStorage
	if c.storage == "memory" || c.storage == "m" {
		st = nats.MemoryStorage
	}

	placement := &nats.Placement{Cluster: c.placementCluster}
	if len(c.placementTags) > 0 {
		placement.Tags = c.placementTags
	}

	obj, err := js.CreateObjectStore(&nats.ObjectStoreConfig{
		Bucket:      c.bucket,
		Description: c.description,
		TTL:         c.ttl,
		Storage:     st,
		Replicas:    int(c.replicas),
		Placement:   placement,
		MaxBytes:    c.maxBucketSize,
		Metadata:    c.metadata,
		Compression: c.compression,
	})
	if err != nil {
		return err
	}

	return c.showBucketInfo(obj)
}

func (c *objCommand) loadBucket() (*nats.Conn, nats.JetStreamContext, nats.ObjectStore, error) {
	nc, js, err := prepareJSHelper()
	if err != nil {
		return nil, nil, nil, err
	}

	if c.bucket == "" {
		known, err := c.knownBuckets(nc)
		if err != nil {
			return nil, nil, nil, err
		}

		if len(known) == 0 {
			return nil, nil, nil, fmt.Errorf("no Object buckets found")
		}

		err = askOne(&survey.Select{
			Message:  "Select a Bucket",
			Options:  known,
			PageSize: selectPageSize(len(known)),
		}, &c.bucket)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	store, err := js.ObjectStore(c.bucket)
	if err != nil {
		return nil, nil, nil, err
	}

	return nc, js, store, err
}

func (c *objCommand) knownBuckets(nc *nats.Conn) ([]string, error) {
	mgr, err := jsm.New(nc)
	if err != nil {
		return nil, err
	}

	streams, err := mgr.StreamNames(nil)
	if err != nil {
		return nil, err
	}

	var found []string
	for _, stream := range streams {
		if jsm.IsObjectBucketStream(stream) {
			found = append(found, strings.TrimPrefix(stream, "OBJ_"))
		}
	}

	return found, nil
}
