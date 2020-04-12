/*
Copyright 2019 The Vitess Authors.

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

package vtgate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golang.org/x/net/context"
	"vitess.io/vitess/go/vt/vterrors"

	"vitess.io/vitess/go/sqltypes"
	"vitess.io/vitess/go/vt/discovery"
	_ "vitess.io/vitess/go/vt/vtgate/vindexes"
	"vitess.io/vitess/go/vt/vttablet/sandboxconn"

	querypb "vitess.io/vitess/go/vt/proto/query"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	vtgatepb "vitess.io/vitess/go/vt/proto/vtgate"
	vtrpcpb "vitess.io/vitess/go/vt/proto/vtrpc"
)

func TestPlanSelectNext(t *testing.T) {
	executor, _, _, sbclookup := createExecutorEnvUsing(planAllTheThings)

	query := "select next :n values from user_seq"
	bv := map[string]*querypb.BindVariable{"n": sqltypes.Int64BindVariable(2)}
	_, err := executorExec(executor, query, bv)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           query,
		BindVariables: map[string]*querypb.BindVariable{"n": sqltypes.Int64BindVariable(2)},
	}}

	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries:\n%v, want\n%v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanSelectDBA(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)

	query := "select * from INFORMATION_SCHEMA.foo"
	_, err := executor.Execute(
		context.Background(),
		"TestSelectDBA",
		NewSafeSession(&vtgatepb.Session{TargetString: "TestExecutor"}),
		query,
		map[string]*querypb.BindVariable{},
	)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           query,
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
}

func TestPlanUnsharded(t *testing.T) {
	executor, _, _, sbclookup := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select id from music_user_map where id = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from music_user_map where id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanUnshardedComments(t *testing.T) {
	executor, _, _, sbclookup := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "/* leading */ select id from music_user_map where id = 1 /* trailing */", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "/* leading */ select id from music_user_map where id = 1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}

	_, err = executorExec(executor, "update music_user_map set id = 1 /* trailing */", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "/* leading */ select id from music_user_map where id = 1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql:           "update music_user_map set id = 1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}

	sbclookup.Queries = nil
	_, err = executorExec(executor, "delete from music_user_map /* trailing */", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "delete from music_user_map /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}

	sbclookup.Queries = nil
	_, err = executorExec(executor, "insert into music_user_map values (1) /* trailing */", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "insert into music_user_map values (1) /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanStreamUnsharded(t *testing.T) {
	executor, _, _, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select id from music_user_map where id = 1"
	result, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantResult := sandboxconn.StreamRowResult
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
	testQueryLog(t, logChan, "TestExecuteStream", "SELECT", sql, 1)
}

func TestPlanStreamBuffering(t *testing.T) {
	executor, _, _, sbclookup := createExecutorEnvUsing(planAllTheThings)

	// This test is similar to TestStreamUnsharded except that it returns a Result > 10 bytes,
	// such that the splitting of the Result into multiple Result responses gets tested.
	sbclookup.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.VarChar},
		},
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewVarChar("01234567890123456789"),
		}, {
			sqltypes.NewInt32(2),
			sqltypes.NewVarChar("12345678901234567890"),
		}},
	}})

	results := make(chan *sqltypes.Result, 10)
	err := executor.StreamExecute(
		context.Background(),
		"TestStreamBuffering",
		NewSafeSession(masterSession),
		"select id from music_user_map where id = 1",
		nil,
		querypb.Target{
			TabletType: topodatapb.TabletType_MASTER,
		},
		func(qr *sqltypes.Result) error {
			results <- qr
			return nil
		},
	)
	close(results)
	require.NoError(t, err)
	wantResults := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.VarChar},
		},
	}, {
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewVarChar("01234567890123456789"),
		}},
	}, {
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(2),
			sqltypes.NewVarChar("12345678901234567890"),
		}},
	}}
	var gotResults []*sqltypes.Result
	for r := range results {
		gotResults = append(gotResults, r)
	}
	if !reflect.DeepEqual(gotResults, wantResults) {
		t.Logf("len: %d", len(gotResults))
		for i := range gotResults {
			t.Errorf("Buffered streaming:\n%v, want\n%v", gotResults[i], wantResults[i])
		}
	}
}

func TestPlanSelectLastInsertId(t *testing.T) {
	masterSession.LastInsertId = 52
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select last_insert_id()"
	_, err := executorExec(executor, sql, map[string]*querypb.BindVariable{})
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select :__lastInsertId as `last_insert_id()` from dual",
		BindVariables: map[string]*querypb.BindVariable{"__lastInsertId": sqltypes.Uint64BindVariable(52)},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanSelectUserDefindVariable(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select @foo"
	masterSession = &vtgatepb.Session{UserDefinedVariables: createMap([]string{"foo"}, []interface{}{"bar"})}
	_, err := executorExec(executor, sql, map[string]*querypb.BindVariable{})
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select :__vtudvfoo as `@foo` from dual",
		BindVariables: map[string]*querypb.BindVariable{"__vtudvfoo": sqltypes.StringBindVariable("bar")},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanFoundRows(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	// run this extra query so we can assert on the number of rows found
	_, err := executorExec(executor, "select 42", map[string]*querypb.BindVariable{})
	require.NoError(t, err)

	sql := "select found_rows()"
	_, err = executorExec(executor, sql, map[string]*querypb.BindVariable{})
	require.NoError(t, err)
	expected := &querypb.BoundQuery{
		Sql:           "select :__vtfrows as `found_rows()` from dual",
		BindVariables: map[string]*querypb.BindVariable{"__vtfrows": sqltypes.Uint64BindVariable(1)},
	}

	assert.Equal(t, expected, sbc1.Queries[1])
}

func TestPlanSelectLastInsertIdInUnion(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	sql := "select last_insert_id() as id union select id from user"
	_, err := executorExec(executor, sql, map[string]*querypb.BindVariable{})
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select :__lastInsertId as id from dual union select id from user",
		BindVariables: map[string]*querypb.BindVariable{"__lastInsertId": sqltypes.Uint64BindVariable(0)},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanSelectLastInsertIdInWhere(t *testing.T) {
	executor, _, _, lookup := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select id from music_user_map where id = last_insert_id()"
	_, err := executorExec(executor, sql, map[string]*querypb.BindVariable{})
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from music_user_map where id = :__lastInsertId",
		BindVariables: map[string]*querypb.BindVariable{"__lastInsertId": sqltypes.Uint64BindVariable(0)},
	}}

	assert.Equal(t, wantQueries, lookup.Queries)
}

func TestPlanLastInsertIDInVirtualTable(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	_, err := executorExec(executor, "select * from (select last_insert_id()) as t", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select * from (select :__lastInsertId as `last_insert_id()` from dual) as t",
		BindVariables: map[string]*querypb.BindVariable{"__lastInsertId": sqltypes.Uint64BindVariable(0)},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanLastInsertIDInSubQueryExpression(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	_, err := executorExec(executor, "select (select last_insert_id()) as x", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select (select :__lastInsertId as `last_insert_id()` from dual) as x from dual",
		BindVariables: map[string]*querypb.BindVariable{"__lastInsertId": sqltypes.Uint64BindVariable(0)},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanSelectDatabase(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true
	sql := "select database()"
	newSession := *masterSession
	session := NewSafeSession(&newSession)
	session.TargetString = "TestExecutor@master"
	_, err := executor.Execute(
		context.Background(),
		"TestExecute",
		session,
		sql,
		map[string]*querypb.BindVariable{})

	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select :__vtdbname as `database()` from dual",
		BindVariables: map[string]*querypb.BindVariable{"__vtdbname": sqltypes.StringBindVariable("TestExecutor")},
	}}

	assert.Equal(t, wantQueries, sbc1.Queries)
}

func TestPlanSelectBindvars(t *testing.T) {
	executor, sbc1, sbc2, lookup := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select id from user where id = :id"
	_, err := executorExec(executor, sql, map[string]*querypb.BindVariable{
		"id": sqltypes.Int64BindVariable(1),
	})
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from user where id = :id",
		BindVariables: map[string]*querypb.BindVariable{"id": sqltypes.Int64BindVariable(1)},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil
	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 1)

	// Test with StringBindVariable
	sql = "select id from user where name in (:name1, :name2)"
	_, err = executorExec(executor, sql, map[string]*querypb.BindVariable{
		"name1": sqltypes.StringBindVariable("foo1"),
		"name2": sqltypes.StringBindVariable("foo2"),
	})
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where name in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"name1":  sqltypes.StringBindVariable("foo1"),
			"name2":  sqltypes.StringBindVariable("foo2"),
			"__vals": sqltypes.TestBindVariable([]interface{}{"foo1", "foo2"}),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	sbc1.Queries = nil
	testQueryLog(t, logChan, "VindexLookup", "SELECT", "select user_id from name_user_map where name = :name", 1)
	testQueryLog(t, logChan, "VindexLookup", "SELECT", "select user_id from name_user_map where name = :name", 1)
	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 1)

	// Test with BytesBindVariable
	sql = "select id from user where name in (:name1, :name2)"
	_, err = executorExec(executor, sql, map[string]*querypb.BindVariable{
		"name1": sqltypes.BytesBindVariable([]byte("foo1")),
		"name2": sqltypes.BytesBindVariable([]byte("foo2")),
	})
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where name in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"name1":  sqltypes.BytesBindVariable([]byte("foo1")),
			"name2":  sqltypes.BytesBindVariable([]byte("foo2")),
			"__vals": sqltypes.TestBindVariable([]interface{}{[]byte("foo1"), []byte("foo2")}),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}

	testQueryLog(t, logChan, "VindexLookup", "SELECT", "select user_id from name_user_map where name = :name", 1)
	testQueryLog(t, logChan, "VindexLookup", "SELECT", "select user_id from name_user_map where name = :name", 1)
	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 1)

	// Test no match in the lookup vindex
	sbc1.Queries = nil
	lookup.Queries = nil
	lookup.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "user_id", Type: sqltypes.Int32},
		},
		RowsAffected: 0,
		InsertID:     0,
		Rows:         [][]sqltypes.Value{},
	}})

	sql = "select id from user where name = :name"
	_, err = executorExec(executor, sql, map[string]*querypb.BindVariable{
		"name": sqltypes.StringBindVariable("nonexistent"),
	})
	require.NoError(t, err)

	// When there are no matching rows in the vindex, vtgate still needs the field info
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"name": sqltypes.StringBindVariable("nonexistent"),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}

	wantLookupQueries := []*querypb.BoundQuery{{
		Sql: "select user_id from name_user_map where name = :name",
		BindVariables: map[string]*querypb.BindVariable{
			"name": sqltypes.StringBindVariable("nonexistent"),
		},
	}}
	if !reflect.DeepEqual(lookup.Queries, wantLookupQueries) {
		t.Errorf("lookup.Queries: %+v, want %+v\n", lookup.Queries, wantLookupQueries)
	}

	testQueryLog(t, logChan, "VindexLookup", "SELECT", "select user_id from name_user_map where name = :name", 1)
	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 1)

}

func TestPlanSelectEqual(t *testing.T) {
	executor, sbc1, sbc2, sbclookup := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select id from user where id = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from user where id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil

	_, err = executorExec(executor, "select id from user where id = 3", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select id from user where id = 3",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}
	if execCount := sbc1.ExecCount.Get(); execCount != 1 {
		t.Errorf("sbc1.ExecCount: %v, want 1\n", execCount)
	}
	if sbc1.Queries != nil {
		t.Errorf("sbc1.Queries: %+v, want nil\n", sbc1.Queries)
	}
	sbc2.Queries = nil

	_, err = executorExec(executor, "select id from user where id = '3'", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select id from user where id = '3'",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}
	if execCount := sbc1.ExecCount.Get(); execCount != 1 {
		t.Errorf("sbc1.ExecCount: %v, want 1\n", execCount)
	}
	if sbc1.Queries != nil {
		t.Errorf("sbc1.Queries: %+v, want nil\n", sbc1.Queries)
	}
	sbc2.Queries = nil

	_, err = executorExec(executor, "select id from user where name = 'foo'", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select id from user where name = 'foo'",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select user_id from name_user_map where name = :name",
		BindVariables: map[string]*querypb.BindVariable{
			"name": sqltypes.BytesBindVariable([]byte("foo")),
		},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanSelectDual(t *testing.T) {
	executor, sbc1, _, lookup := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select @@aa.bb from dual", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select @@aa.bb from dual",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}

	_, err = executorExec(executor, "select @@aa.bb from TestUnsharded.dual", nil)
	require.NoError(t, err)
	if !reflect.DeepEqual(lookup.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
}

func TestPlanSelectComments(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "/* leading */ select id from user where id = 1 /* trailing */", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "/* leading */ select id from user where id = 1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil
}

func TestPlanSelectNormalize(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	executor.normalize = true

	_, err := executorExec(executor, "/* leading */ select id from user where id = 1 /* trailing */", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql: "/* leading */ select id from user where id = :vtg1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{
			"vtg1": sqltypes.TestBindVariable(int64(1)),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil

	// Force the query to go to the "wrong" shard and ensure that normalization still happens
	masterSession.TargetString = "TestExecutor/40-60"
	_, err = executorExec(executor, "/* leading */ select id from user where id = 1 /* trailing */", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql: "/* leading */ select id from user where id = :vtg1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{
			"vtg1": sqltypes.TestBindVariable(int64(1)),
		},
	}}
	if sbc1.Queries != nil {
		t.Errorf("sbc1.Queries: %+v, want nil\n", sbc1.Queries)
	}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}
	sbc2.Queries = nil
	masterSession.TargetString = ""
}

func TestPlanSelectCaseSensitivity(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select Id from user where iD = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select Id from user where iD = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil
}

func TestPlanStreamSelectEqual(t *testing.T) {
	executor, _, _, _ := createExecutorEnvUsing(planAllTheThings)

	sql := "select id from user where id = 1"
	result, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantResult := sandboxconn.StreamRowResult
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanSelectKeyRange(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select krcol_unique, krcol from keyrange_table where krcol = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select krcol_unique, krcol from keyrange_table where krcol = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil
}

func TestPlanSelectKeyRangeUnique(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)

	_, err := executorExec(executor, "select krcol_unique, krcol from keyrange_table where krcol_unique = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select krcol_unique, krcol from keyrange_table where krcol_unique = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
	sbc1.Queries = nil
}

func TestPlanSelectIN(t *testing.T) {
	executor, sbc1, sbc2, sbclookup := createExecutorEnvUsing(planAllTheThings)

	// Constant in IN clause is just a number, not a bind variable.
	_, err := executorExec(executor, "select id from user where id in (1)", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql: "select id from user where id in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"__vals": sqltypes.TestBindVariable([]interface{}{int64(1)}),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}

	// Constants in IN clause are just numbers, not bind variables.
	// They result in two different queries on two shards.
	sbc1.Queries = nil
	sbc2.Queries = nil
	_, err = executorExec(executor, "select id from user where id in (1, 3)", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where id in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"__vals": sqltypes.TestBindVariable([]interface{}{int64(1)}),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where id in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"__vals": sqltypes.TestBindVariable([]interface{}{int64(3)}),
		},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}

	// In is a bind variable list, that will end up on two shards.
	// This is using an []interface{} for the bind variable list.
	sbc1.Queries = nil
	sbc2.Queries = nil
	_, err = executorExec(executor, "select id from user where id in ::vals", map[string]*querypb.BindVariable{
		"vals": sqltypes.TestBindVariable([]interface{}{int64(1), int64(3)}),
	})
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where id in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"__vals": sqltypes.TestBindVariable([]interface{}{int64(1)}),
			"vals":   sqltypes.TestBindVariable([]interface{}{int64(1), int64(3)}),
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select id from user where id in ::__vals",
		BindVariables: map[string]*querypb.BindVariable{
			"__vals": sqltypes.TestBindVariable([]interface{}{int64(3)}),
			"vals":   sqltypes.TestBindVariable([]interface{}{int64(1), int64(3)}),
		},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}

	// Convert a non-list bind variable.
	sbc1.Queries = nil
	sbc2.Queries = nil
	_, err = executorExec(executor, "select id from user where name = 'foo'", nil)
	require.NoError(t, err)
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select id from user where name = 'foo'",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql: "select user_id from name_user_map where name = :name",
		BindVariables: map[string]*querypb.BindVariable{
			"name": sqltypes.BytesBindVariable([]byte("foo")),
		},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanStreamSelectIN(t *testing.T) {
	executor, _, _, sbclookup := createExecutorEnvUsing(planAllTheThings)

	sql := "select id from user where id in (1)"
	result, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantResult := sandboxconn.StreamRowResult
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	sql = "select id from user where id in (1, 3)"
	result, err = executorStream(executor, sql)
	require.NoError(t, err)
	wantResult = &sqltypes.Result{
		Fields: sandboxconn.StreamRowResult.Fields,
		Rows: [][]sqltypes.Value{
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
		},
		RowsAffected: 0,
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	sql = "select id from user where name = 'foo'"
	result, err = executorStream(executor, sql)
	require.NoError(t, err)
	wantResult = sandboxconn.StreamRowResult
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	wantQueries := []*querypb.BoundQuery{{
		Sql: "select user_id from name_user_map where name = :name",
		BindVariables: map[string]*querypb.BindVariable{
			"name": sqltypes.BytesBindVariable([]byte("foo")),
		},
	}}
	if !reflect.DeepEqual(sbclookup.Queries, wantQueries) {
		t.Errorf("sbclookup.Queries: %+v, want %+v\n", sbclookup.Queries, wantQueries)
	}
}

func TestPlanSelectScatter(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for _, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	_, err := executorExec(executor, "select id from user", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from user",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}
	testQueryLog(t, logChan, "TestExecute", "SELECT", wantQueries[0].Sql, 8)
}

func TestPlanSelectScatterPartial(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for _, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		conns = append(conns, sbc)
	}

	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	// Fail 1 of N without the directive fails the whole operation
	conns[2].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	results, err := executorExec(executor, "select id from user", nil)
	wantErr := "TestExecutor.40-60.master, used tablet: aa-0 (40-60)"
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("want error %v, got %v", wantErr, err)
	}
	if vterrors.Code(err) != vtrpcpb.Code_RESOURCE_EXHAUSTED {
		t.Errorf("want error code Code_RESOURCE_EXHAUSTED, but got %v", vterrors.Code(err))
	}
	if results != nil {
		t.Errorf("want nil results, got %v", results)
	}
	testQueryLog(t, logChan, "TestExecute", "SELECT", "select id from user", 8)

	// Fail 1 of N with the directive succeeds with 7 rows
	results, err = executorExec(executor, "select /*vt+ SCATTER_ERRORS_AS_WARNINGS=1 */ id from user", nil)
	require.NoError(t, err)
	if results == nil || len(results.Rows) != 7 {
		t.Errorf("want 7 results, got %v", results)
	}
	testQueryLog(t, logChan, "TestExecute", "SELECT", "select /*vt+ SCATTER_ERRORS_AS_WARNINGS=1 */ id from user", 8)

	// Even if all shards fail the operation succeeds with 0 rows
	conns[0].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[1].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[3].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[4].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[5].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[6].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000
	conns[7].MustFailCodes[vtrpcpb.Code_RESOURCE_EXHAUSTED] = 1000

	results, err = executorExec(executor, "select /*vt+ SCATTER_ERRORS_AS_WARNINGS=1 */ id from user", nil)
	require.NoError(t, err)
	if results == nil || len(results.Rows) != 0 {
		t.Errorf("want 0 result rows, got %v", results)
	}
	testQueryLog(t, logChan, "TestExecute", "SELECT", "select /*vt+ SCATTER_ERRORS_AS_WARNINGS=1 */ id from user", 8)
}

func TestPlanStreamSelectScatter(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	for _, shard := range shards {
		_ = hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	sql := "select id from user"
	result, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantResult := &sqltypes.Result{
		Fields: sandboxconn.SingleRowResult.Fields,
		Rows: [][]sqltypes.Value{
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
			sandboxconn.StreamRowResult.Rows[0],
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

// TestSelectScatterOrderBy will run an ORDER BY query that will scatter out to 8 shards and return the 8 rows (one per shard) sorted.
func TestPlanSelectScatterOrderBy(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col1", Type: sqltypes.Int32},
				{Name: "col2", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				// i%4 ensures that there are duplicates across shards.
				// This will allow us to test that cross-shard ordering
				// still works correctly.
				sqltypes.NewInt32(int32(i % 4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col1, col2 from user order by col2 desc"
	gotResult, err := executorExec(executor, query, nil)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           query,
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col1", Type: sqltypes.Int32},
			{Name: "col2", Type: sqltypes.Int32},
		},
		RowsAffected: 8,
		InsertID:     0,
	}
	for i := 0; i < 4; i++ {
		// There should be a duplicate for each row returned.
		for j := 0; j < 2; j++ {
			row := []sqltypes.Value{
				sqltypes.NewInt32(1),
				sqltypes.NewInt32(int32(3 - i)),
			}
			wantResult.Rows = append(wantResult.Rows, row)
		}
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

// TestSelectScatterOrderByVarChar will run an ORDER BY query that will scatter out to 8 shards and return the 8 rows (one per shard) sorted.
func TestPlanSelectScatterOrderByVarChar(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col1", Type: sqltypes.Int32},
				{Name: "textcol", Type: sqltypes.VarChar},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				// i%4 ensures that there are duplicates across shards.
				// This will allow us to test that cross-shard ordering
				// still works correctly.
				sqltypes.NewVarChar(fmt.Sprintf("%d", i%4)),
				sqltypes.NewVarBinary(fmt.Sprintf("%d", i%4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col1, textcol from user order by textcol desc"
	gotResult, err := executorExec(executor, query, nil)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select col1, textcol, weight_string(textcol) from user order by textcol desc",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col1", Type: sqltypes.Int32},
			{Name: "textcol", Type: sqltypes.VarChar},
		},
		RowsAffected: 8,
		InsertID:     0,
	}
	for i := 0; i < 4; i++ {
		// There should be a duplicate for each row returned.
		for j := 0; j < 2; j++ {
			row := []sqltypes.Value{
				sqltypes.NewInt32(1),
				sqltypes.NewVarChar(fmt.Sprintf("%d", 3-i)),
			}
			wantResult.Rows = append(wantResult.Rows, row)
		}
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

func TestPlanStreamSelectScatterOrderBy(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "id", Type: sqltypes.Int32},
				{Name: "col", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				sqltypes.NewInt32(int32(i % 4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select id, col from user order by col desc"
	gotResult, err := executorStream(executor, query)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           query,
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
	}
	for i := 0; i < 4; i++ {
		row := []sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(int32(3 - i)),
		}
		wantResult.Rows = append(wantResult.Rows, row, row)
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

func TestPlanStreamSelectScatterOrderByVarChar(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "id", Type: sqltypes.Int32},
				{Name: "textcol", Type: sqltypes.VarChar},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				sqltypes.NewVarChar(fmt.Sprintf("%d", i%4)),
				sqltypes.NewVarBinary(fmt.Sprintf("%d", i%4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select id, textcol from user order by textcol desc"
	gotResult, err := executorStream(executor, query)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id, textcol, weight_string(textcol) from user order by textcol desc",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "textcol", Type: sqltypes.VarChar},
		},
	}
	for i := 0; i < 4; i++ {
		row := []sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewVarChar(fmt.Sprintf("%d", 3-i)),
		}
		wantResult.Rows = append(wantResult.Rows, row, row)
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

// TestSelectScatterAggregate will run an aggregate query that will scatter out to 8 shards and return 4 aggregated rows.
func TestPlanSelectScatterAggregate(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col", Type: sqltypes.Int32},
				{Name: "sum(foo)", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(int32(i % 4)),
				sqltypes.NewInt32(int32(i)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col, sum(foo) from user group by col"
	gotResult, err := executorExec(executor, query, nil)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           query + " order by col asc",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col", Type: sqltypes.Int32},
			{Name: "sum(foo)", Type: sqltypes.Int32},
		},
		RowsAffected: 4,
		InsertID:     0,
	}
	for i := 0; i < 4; i++ {
		row := []sqltypes.Value{
			sqltypes.NewInt32(int32(i)),
			sqltypes.NewInt32(int32(i*2 + 4)),
		}
		wantResult.Rows = append(wantResult.Rows, row)
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

func TestPlanStreamSelectScatterAggregate(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col", Type: sqltypes.Int32},
				{Name: "sum(foo)", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(int32(i % 4)),
				sqltypes.NewInt32(int32(i)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col, sum(foo) from user group by col"
	gotResult, err := executorStream(executor, query)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           query + " order by col asc",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("conn.Queries = %#v, want %#v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col", Type: sqltypes.Int32},
			{Name: "sum(foo)", Type: sqltypes.Int32},
		},
	}
	for i := 0; i < 4; i++ {
		row := []sqltypes.Value{
			sqltypes.NewInt32(int32(i)),
			sqltypes.NewInt32(int32(i*2 + 4)),
		}
		wantResult.Rows = append(wantResult.Rows, row)
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

// TestSelectScatterLimit will run a limit query (ordered for consistency) against
// a scatter route and verify that the limit primitive works as intended.
func TestPlanSelectScatterLimit(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col1", Type: sqltypes.Int32},
				{Name: "col2", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				sqltypes.NewInt32(int32(i % 4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col1, col2 from user order by col2 desc limit 3"
	gotResult, err := executorExec(executor, query, nil)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select col1, col2 from user order by col2 desc limit :__upper_limit",
		BindVariables: map[string]*querypb.BindVariable{"__upper_limit": sqltypes.Int64BindVariable(3)},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("got: conn.Queries = %v, want: %v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col1", Type: sqltypes.Int32},
			{Name: "col2", Type: sqltypes.Int32},
		},
		RowsAffected: 3,
		InsertID:     0,
	}
	wantResult.Rows = append(wantResult.Rows,
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		},
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		},
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(2),
		})

	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

// TestStreamSelectScatterLimit will run a streaming limit query (ordered for consistency) against
// a scatter route and verify that the limit primitive works as intended.
func TestPlanStreamSelectScatterLimit(t *testing.T) {
	// Special setup: Don't use createExecutorEnv.
	cell := "aa"
	hc := discovery.NewFakeHealthCheck()
	s := createSandbox("TestExecutor")
	s.VSchema = executorVSchema
	getSandbox(KsTestUnsharded).VSchema = unshardedVSchema
	serv := new(sandboxTopo)
	resolver := newTestResolver(hc, serv, cell)
	shards := []string{"-20", "20-40", "40-60", "60-80", "80-a0", "a0-c0", "c0-e0", "e0-"}
	var conns []*sandboxconn.SandboxConn
	for i, shard := range shards {
		sbc := hc.AddTestTablet(cell, shard, 1, "TestExecutor", shard, topodatapb.TabletType_MASTER, true, 1, nil)
		sbc.SetResults([]*sqltypes.Result{{
			Fields: []*querypb.Field{
				{Name: "col1", Type: sqltypes.Int32},
				{Name: "col2", Type: sqltypes.Int32},
			},
			RowsAffected: 1,
			InsertID:     0,
			Rows: [][]sqltypes.Value{{
				sqltypes.NewInt32(1),
				sqltypes.NewInt32(int32(i % 4)),
			}},
		}})
		conns = append(conns, sbc)
	}
	executor := NewExecutor(context.Background(), serv, cell, resolver, false, testBufferSize, testCacheSize)

	query := "select col1, col2 from user order by col2 desc limit 3"
	gotResult, err := executorStream(executor, query)
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select col1, col2 from user order by col2 desc limit :__upper_limit",
		BindVariables: map[string]*querypb.BindVariable{"__upper_limit": sqltypes.Int64BindVariable(3)},
	}}
	for _, conn := range conns {
		if !reflect.DeepEqual(conn.Queries, wantQueries) {
			t.Errorf("got: conn.Queries = %v, want: %v", conn.Queries, wantQueries)
		}
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col1", Type: sqltypes.Int32},
			{Name: "col2", Type: sqltypes.Int32},
		},
	}
	wantResult.Rows = append(wantResult.Rows,
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		},
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		},
		[]sqltypes.Value{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(2),
		})

	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("scatter order by:\n%v, want\n%v", gotResult, wantResult)
	}
}

// TODO(sougou): stream and non-stream testing are very similar.
// Could reuse code,
func TestPlanSimpleJoin(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select u1.id, u2.id from user u1 join user u2 where u1.id = 1 and u2.id = 3"
	result, err := executorExec(executor, sql, nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select u2.id from user as u2 where u2.id = 3",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			sandboxconn.SingleRowResult.Fields[0],
			sandboxconn.SingleRowResult.Fields[0],
		},
		Rows: [][]sqltypes.Value{
			{
				sandboxconn.SingleRowResult.Rows[0][0],
				sandboxconn.SingleRowResult.Rows[0][0],
			},
		},
		RowsAffected: 1,
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 2)
}

func TestPlanJoinComments(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select u1.id, u2.id from user u1 join user u2 where u1.id = 1 and u2.id = 3 /* trailing */"
	_, err := executorExec(executor, sql, nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id from user as u1 where u1.id = 1 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select u2.id from user as u2 where u2.id = 3 /* trailing */",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}

	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 2)
}

func TestPlanSimpleJoinStream(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select u1.id, u2.id from user u1 join user u2 where u1.id = 1 and u2.id = 3"
	result, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantQueries = []*querypb.BoundQuery{{
		Sql:           "select u2.id from user as u2 where u2.id = 3",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc2.Queries, wantQueries) {
		t.Errorf("sbc2.Queries: %+v, want %+v\n", sbc2.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			sandboxconn.SingleRowResult.Fields[0],
			sandboxconn.SingleRowResult.Fields[0],
		},
		Rows: [][]sqltypes.Value{
			{
				sandboxconn.SingleRowResult.Rows[0][0],
				sandboxconn.SingleRowResult.Rows[0][0],
			},
		},
		RowsAffected: 0,
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	testQueryLog(t, logChan, "TestExecuteStream", "SELECT", sql, 2)
}

func TestPlanVarJoin(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	sql := "select u1.id, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1"
	_, err := executorExec(executor, sql, nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	// We have to use string representation because bindvars type is too complex.
	got := fmt.Sprintf("%+v", sbc2.Queries)
	want := `[sql:"select u2.id from user as u2 where u2.id = :u1_col" bind_variables:<key:"u1_col" value:<type:INT32 value:"3" > > ]`
	if got != want {
		t.Errorf("sbc2.Queries: %s, want %s\n", got, want)
	}

	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 2)
}

func TestPlanVarJoinStream(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	sql := "select u1.id, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1"
	_, err := executorStream(executor, sql)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	// We have to use string representation because bindvars type is too complex.
	got := fmt.Sprintf("%+v", sbc2.Queries)
	want := `[sql:"select u2.id from user as u2 where u2.id = :u1_col" bind_variables:<key:"u1_col" value:<type:INT32 value:"3" > > ]`
	if got != want {
		t.Errorf("sbc2.Queries: %s, want %s\n", got, want)
	}

	testQueryLog(t, logChan, "TestExecuteStream", "SELECT", sql, 2)
}

func TestPlanLeftJoin(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	emptyResult := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}}
	sbc1.SetResults(result1)
	sbc2.SetResults(emptyResult)
	sql := "select u1.id, u2.id from user u1 left join user u2 on u2.id = u1.col where u1.id = 1"
	result, err := executorExec(executor, sql, nil)
	require.NoError(t, err)
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			sandboxconn.SingleRowResult.Fields[0],
			sandboxconn.SingleRowResult.Fields[0],
		},
		Rows: [][]sqltypes.Value{
			{
				sandboxconn.SingleRowResult.Rows[0][0],
				{},
			},
		},
		RowsAffected: 1,
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}

	testQueryLog(t, logChan, "TestExecute", "SELECT", sql, 2)

}

func TestPlanLeftJoinStream(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	emptyResult := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}}
	sbc1.SetResults(result1)
	sbc2.SetResults(emptyResult)
	result, err := executorStream(executor, "select u1.id, u2.id from user u1 left join user u2 on u2.id = u1.col where u1.id = 1")
	require.NoError(t, err)
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			sandboxconn.SingleRowResult.Fields[0],
			sandboxconn.SingleRowResult.Fields[0],
		},
		Rows: [][]sqltypes.Value{
			{
				sandboxconn.SingleRowResult.Rows[0][0],
				{},
			},
		},
		RowsAffected: 0,
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanEmptyJoin(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	// Empty result requires a field query for the second part of join,
	// which is sent to shard 0.
	sbc1.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}})
	result, err := executorExec(executor, "select u1.id, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql: "select u2.id from user as u2 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"u1_col": sqltypes.NullBindVariable,
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries:\n%v, want\n%v\n", sbc1.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanEmptyJoinStream(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	// Empty result requires a field query for the second part of join,
	// which is sent to shard 0.
	sbc1.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}})
	result, err := executorStream(executor, "select u1.id, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1")
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql: "select u2.id from user as u2 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"u1_col": sqltypes.NullBindVariable,
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanEmptyJoinRecursive(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	// Make sure it also works recursively.
	sbc1.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}})
	result, err := executorExec(executor, "select u1.id, u2.id, u3.id from user u1 join (user u2 join user u3 on u3.id = u2.col) where u1.id = 1", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql:           "select u2.id, u2.col from user as u2 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql: "select u3.id from user as u3 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"u2_col": sqltypes.NullBindVariable,
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries:\n%+v, want\n%+v\n", sbc1.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanEmptyJoinRecursiveStream(t *testing.T) {
	executor, sbc1, _, _ := createExecutorEnvUsing(planAllTheThings)
	// Make sure it also works recursively.
	sbc1.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
	}, {
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
	}})
	result, err := executorStream(executor, "select u1.id, u2.id, u3.id from user u1 join (user u2 join user u3 on u3.id = u2.col) where u1.id = 1")
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql:           "select u2.id, u2.col from user as u2 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql: "select u3.id from user as u3 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"u2_col": sqltypes.NullBindVariable,
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanCrossShardSubquery(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	result, err := executorExec(executor, "select id1 from (select u1.id id1, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1) as t", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id as id1, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	// We have to use string representation because bindvars type is too complex.
	got := fmt.Sprintf("%+v", sbc2.Queries)
	want := `[sql:"select u2.id from user as u2 where u2.id = :u1_col" bind_variables:<key:"u1_col" value:<type:INT32 value:"3" > > ]`
	if got != want {
		t.Errorf("sbc2.Queries: %s, want %s\n", got, want)
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
		}},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanCrossShardSubqueryStream(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
		RowsAffected: 1,
		InsertID:     0,
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
			sqltypes.NewInt32(3),
		}},
	}}
	sbc1.SetResults(result1)
	result, err := executorStream(executor, "select id1 from (select u1.id id1, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1) as t")
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id as id1, u1.col from user as u1 where u1.id = 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries:\n%+v, want\n%+v\n", sbc1.Queries, wantQueries)
	}
	// We have to use string representation because bindvars type is too complex.
	got := fmt.Sprintf("%+v", sbc2.Queries)
	want := `[sql:"select u2.id from user as u2 where u2.id = :u1_col" bind_variables:<key:"u1_col" value:<type:INT32 value:"3" > > ]`
	if got != want {
		t.Errorf("sbc2.Queries:\n%s, want\n%s\n", got, want)
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
		},
		Rows: [][]sqltypes.Value{{
			sqltypes.NewInt32(1),
		}},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanCrossShardSubqueryGetFields(t *testing.T) {
	executor, sbc1, _, sbclookup := createExecutorEnvUsing(planAllTheThings)
	sbclookup.SetResults([]*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "col", Type: sqltypes.Int32},
		},
	}})
	result1 := []*sqltypes.Result{{
		Fields: []*querypb.Field{
			{Name: "id", Type: sqltypes.Int32},
			{Name: "col", Type: sqltypes.Int32},
		},
	}}
	sbc1.SetResults(result1)
	result, err := executorExec(executor, "select main1.col, t.id1 from main1 join (select u1.id id1, u2.id from user u1 join user u2 on u2.id = u1.col where u1.id = 1) as t", nil)
	require.NoError(t, err)
	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select u1.id as id1, u1.col from user as u1 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{},
	}, {
		Sql: "select u2.id from user as u2 where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{
			"u1_col": sqltypes.NullBindVariable,
		},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries:\n%+v, want\n%+v\n", sbc1.Queries, wantQueries)
	}

	wantResult := &sqltypes.Result{
		Fields: []*querypb.Field{
			{Name: "col", Type: sqltypes.Int32},
			{Name: "id", Type: sqltypes.Int32},
		},
	}
	if !result.Equal(wantResult) {
		t.Errorf("result: %+v, want %+v", result, wantResult)
	}
}

func TestPlanSelectBindvarswithPrepare(t *testing.T) {
	executor, sbc1, sbc2, _ := createExecutorEnvUsing(planAllTheThings)
	logChan := QueryLogger.Subscribe("Test")
	defer QueryLogger.Unsubscribe(logChan)

	sql := "select id from user where id = :id"
	_, err := executorPrepare(executor, sql, map[string]*querypb.BindVariable{
		"id": sqltypes.Int64BindVariable(1),
	})
	require.NoError(t, err)

	wantQueries := []*querypb.BoundQuery{{
		Sql:           "select id from user where 1 != 1",
		BindVariables: map[string]*querypb.BindVariable{"id": sqltypes.Int64BindVariable(1)},
	}}
	if !reflect.DeepEqual(sbc1.Queries, wantQueries) {
		t.Errorf("sbc1.Queries: %+v, want %+v\n", sbc1.Queries, wantQueries)
	}
	if sbc2.Queries != nil {
		t.Errorf("sbc2.Queries: %+v, want nil\n", sbc2.Queries)
	}
}