package tsdb

import (
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/influxdb/influxdb/influxql"
	"github.com/influxdb/influxdb/meta"
)

var shardID = uint64(1)

func TestWritePointsAndExecuteQuery(t *testing.T) {
	store, executor := testStoreAndExecutor()
	defer os.RemoveAll(store.path)

	pt := NewPoint(
		"cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": 1.0},
		time.Unix(1, 2),
	)

	err := store.WriteToShard(shardID, []Point{pt})
	if err != nil {
		t.Fatalf(err.Error())
	}

	pt.SetTime(time.Unix(2, 3))
	err = store.WriteToShard(shardID, []Point{pt})
	if err != nil {
		t.Fatalf(err.Error())
	}

	got := executeAndGetJSON("select * from cpu", executor)
	exepected := `[{"series":[{"name":"cpu","tags":{"host":"server"},"columns":["time","value"],"values":[["1970-01-01T00:00:01.000000002Z",1],["1970-01-01T00:00:02.000000003Z",1]]}]}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}

	store.Close()
	store = NewStore(store.path)
	err = store.Open()
	if err != nil {
		t.Fatalf(err.Error())
	}
	executor.store = store

	got = executeAndGetJSON("select * from cpu", executor)
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}
}

func TestDropSeriesStatement(t *testing.T) {
	store, executor := testStoreAndExecutor()
	defer os.RemoveAll(store.path)

	pt := NewPoint(
		"cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": 1.0},
		time.Unix(1, 2),
	)

	err := store.WriteToShard(shardID, []Point{pt})
	if err != nil {
		t.Fatalf(err.Error())
	}

	got := executeAndGetJSON("select * from cpu", executor)
	exepected := `[{"series":[{"name":"cpu","tags":{"host":"server"},"columns":["time","value"],"values":[["1970-01-01T00:00:01.000000002Z",1]]}]}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}

	got = executeAndGetJSON("drop series from cpu", executor)
	warn("*** ", got)

	got = executeAndGetJSON("select * from cpu", executor)
	exepected = `[{}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}

	got = executeAndGetJSON("show tag keys from cpu", executor)
	exepected = `[{"series":[{"name":"cpu","columns":["tagKey"]}]}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}

	store.Close()
	store = NewStore(store.path)
	store.Open()
	executor.store = store

	got = executeAndGetJSON("select * from cpu", executor)
	exepected = `[{}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}

	got = executeAndGetJSON("show tag keys from cpu", executor)
	exepected = `[{"series":[{"name":"cpu","columns":["tagKey"]}]}]`
	if exepected != got {
		t.Fatalf("exp: %s\ngot: %s", exepected, got)
	}
}

// ensure that authenticate doesn't return an error if the user count is zero and they're attempting
// to create a user.
func TestAuthenticateIfUserCountZeroAndCreateUser(t *testing.T) {
	store, executor := testStoreAndExecutor()
	defer os.RemoveAll(store.path)
	ms := &testMetastore{userCount: 0}
	executor.MetaStore = ms

	if err := executor.Authorize(nil, mustParseQuery("create user foo with password 'asdf' with all privileges"), ""); err != nil {
		t.Fatalf("should have authenticated if no users and attempting to create a user but got error: %s", err.Error())
	}

	if executor.Authorize(nil, mustParseQuery("create user foo with password 'asdf'"), "") == nil {
		t.Fatalf("should have failed authentication if no user given and no users exist for create user query that doesn't grant all privileges")
	}

	if executor.Authorize(nil, mustParseQuery("select * from foo"), "") == nil {
		t.Fatalf("should have failed authentication if no user given and no users exist for any query other than create user")
	}

	ms.userCount = 1

	if executor.Authorize(nil, mustParseQuery("create user foo with password 'asdf'"), "") == nil {
		t.Fatalf("should have failed authentication if no user given and users exist")
	}

	if executor.Authorize(nil, mustParseQuery("select * from foo"), "") == nil {
		t.Fatalf("should have failed authentication if no user given and users exist")
	}
}

func testStoreAndExecutor() (*Store, *QueryExecutor) {
	path, _ := ioutil.TempDir("", "")

	store := NewStore(path)
	err := store.Open()
	if err != nil {
		panic(err)
	}
	database := "foo"
	retentionPolicy := "bar"
	shardID := uint64(1)
	store.CreateShard(database, retentionPolicy, shardID)

	executor := NewQueryExecutor(store)
	executor.MetaStore = &testMetastore{}

	return store, executor
}

func executeAndGetJSON(query string, executor *QueryExecutor) string {
	ch, err := executor.ExecuteQuery(mustParseQuery(query), "foo", 20)
	if err != nil {
		panic(err.Error())
	}

	var results []*influxql.Result
	for r := range ch {
		results = append(results, r)
	}
	return string(mustMarshalJSON(results))
}

type testMetastore struct {
	userCount int
}

func (t *testMetastore) Database(name string) (*meta.DatabaseInfo, error) {
	return &meta.DatabaseInfo{
		Name: name,
		DefaultRetentionPolicy: "foo",
		RetentionPolicies: []meta.RetentionPolicyInfo{
			{
				Name: "bar",
				ShardGroups: []meta.ShardGroupInfo{
					{
						ID:        uint64(1),
						StartTime: time.Now().Add(-time.Hour),
						EndTime:   time.Now().Add(time.Hour),
						Shards: []meta.ShardInfo{
							{
								ID:       uint64(1),
								OwnerIDs: []uint64{1},
							},
						},
					},
				},
			},
		},
	}, nil
}

func (t *testMetastore) Databases() ([]meta.DatabaseInfo, error) {
	db, _ := t.Database("foo")
	return []meta.DatabaseInfo{*db}, nil
}

func (t *testMetastore) User(name string) (*meta.UserInfo, error) { return nil, nil }

func (t *testMetastore) AdminUserExists() (bool, error) { return false, nil }

func (t *testMetastore) Authenticate(username, password string) (*meta.UserInfo, error) {
	return nil, nil
}

func (t *testMetastore) RetentionPolicy(database, name string) (rpi *meta.RetentionPolicyInfo, err error) {
	return &meta.RetentionPolicyInfo{
		Name: "bar",
		ShardGroups: []meta.ShardGroupInfo{
			{
				ID:        uint64(1),
				StartTime: time.Now().Add(-time.Hour),
				EndTime:   time.Now().Add(time.Hour),
				Shards: []meta.ShardInfo{
					{
						ID:       uint64(1),
						OwnerIDs: []uint64{1},
					},
				},
			},
		},
	}, nil
}

func (t *testMetastore) UserCount() (int, error) {
	return t.userCount, nil
}

// MustParseQuery parses an InfluxQL query. Panic on error.
func mustParseQuery(s string) *influxql.Query {
	q, err := influxql.NewParser(strings.NewReader(s)).ParseQuery()
	if err != nil {
		panic(err.Error())
	}
	return q
}
