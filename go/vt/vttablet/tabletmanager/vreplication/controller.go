/*
Copyright 2018 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vreplication

import (
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/golang/protobuf/proto"
	"golang.org/x/net/context"

	"vitess.io/vitess/go/sync2"
	"vitess.io/vitess/go/tb"
	"vitess.io/vitess/go/vt/binlog/binlogplayer"
	"vitess.io/vitess/go/vt/log"
	"vitess.io/vitess/go/vt/mysqlctl"
	"vitess.io/vitess/go/vt/topo"

	binlogdatapb "vitess.io/vitess/go/vt/proto/binlogdata"
)

var retryDelay = flag.Duration("vreplication_retry_delay", 5*time.Second, "delay before retrying a failed binlog connection")

// controller is created by Engine. Members are initialized upfront.
type controller struct {
	dbClientFactory func() binlogplayer.VtClient
	mysqld          mysqlctl.MysqlDaemon
	blpStats        *binlogplayer.Stats

	id           uint32
	source       binlogdatapb.BinlogSource
	startPos     string
	stopPos      string
	tabletPicker *tabletPicker

	cancel context.CancelFunc
	done   chan struct{}

	// The following fields are updated after start. So, they need synchronization.
	sourceTablet sync2.AtomicString
}

func newController(ctx context.Context, params map[string]string, dbClientFactory func() binlogplayer.VtClient, mysqld mysqlctl.MysqlDaemon, ts *topo.Server, cell, tabletTypesStr string) (*controller, error) {
	ct := &controller{
		dbClientFactory: dbClientFactory,
		mysqld:          mysqld,
		blpStats:        binlogplayer.NewStats(),
		done:            make(chan struct{}),
	}

	// id
	id, err := strconv.Atoi(params["id"])
	if err != nil {
		return nil, err
	}
	ct.id = uint32(id)

	// Nothing to do if replication is stopped.
	if params["state"] == binlogplayer.BlpStopped {
		ct.cancel = func() {}
		close(ct.done)
		return ct, nil
	}

	// source, startPos, stopPos
	if err := proto.UnmarshalText(params["source"], &ct.source); err != nil {
		return nil, err
	}
	ct.startPos = params["pos"]
	ct.stopPos = params["stop_pos"]

	// tabletPicker
	if v, ok := params["cell"]; ok {
		cell = v
	}
	if v, ok := params["tablet_types"]; ok {
		tabletTypesStr = v
	}
	tp, err := newTabletPicker(ts, cell, ct.source.Keyspace, ct.source.Shard, tabletTypesStr)
	if err != nil {
		return nil, err
	}
	ct.tabletPicker = tp

	// cancel
	ctx, ct.cancel = context.WithCancel(ctx)

	go ct.run(ctx)

	return ct, nil
}

func (ct *controller) run(ctx context.Context) {
	defer func() {
		log.Infof("%v: stopped", ct)
		ct.tabletPicker.Close()
		close(ct.done)
	}()

	for {
		err := ct.runBlp(ctx)
		if err == nil {
			return
		}
		log.Warningf("%v: %v", ct, err)
		time.Sleep(*retryDelay)
	}
}

func (ct *controller) runBlp(ctx context.Context) (err error) {
	defer func() {
		ct.sourceTablet.Set("")
		if x := recover(); x != nil {
			log.Errorf("%v: caught panic: %v\n%s", ct, x, tb.Stack(4))
			err = fmt.Errorf("panic: %v", x)
		}
	}()

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	// Call this for youtube-specific customization.
	// This should be done every time, in case mysql was restarted.
	if err := ct.mysqld.EnableBinlogPlayback(); err != nil {
		return err
	}

	dbClient := ct.dbClientFactory()
	if err := dbClient.Connect(); err != nil {
		return fmt.Errorf("can't connect to database: %v", err)
	}
	defer dbClient.Close()

	tablet, err := ct.tabletPicker.Pick(ctx)
	if err != nil {
		return err
	}
	ct.sourceTablet.Set(tablet.Alias.String())

	if len(ct.source.Tables) > 0 {
		// Table names can have search patterns. Resolve them against the schema.
		tables, err := mysqlctl.ResolveTables(ct.mysqld, dbClient.DBName(), ct.source.Tables)
		if err != nil {
			return fmt.Errorf("failed to resolve table names: %v", err)
		}

		player, err := binlogplayer.NewBinlogPlayerTables(dbClient, tablet, tables, ct.id, ct.startPos, ct.stopPos, ct.blpStats)
		if err != nil {
			return fmt.Errorf("NewBinlogPlayerTables failed: %v", err)
		}
		return player.ApplyBinlogEvents(ctx)
	}
	player, err := binlogplayer.NewBinlogPlayerKeyRange(dbClient, tablet, ct.source.KeyRange, ct.id, ct.startPos, ct.stopPos, ct.blpStats)
	if err != nil {
		return fmt.Errorf("NewBinlogPlayerKeyRange failed: %v", err)
	}
	return player.ApplyBinlogEvents(ctx)
}

func (ct *controller) Stop() {
	ct.cancel()
	<-ct.done
}
