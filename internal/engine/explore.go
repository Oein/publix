package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/store"
)

// The explorer lets someone look inside a managed database from the
// dashboard: what tables (collections, keys) it holds, a page of rows from
// one, and the result of a read-only query.
//
// Every engine is read with its own client, run inside the database's own
// container — psql, mysql, mongosh, redis-cli — so publix carries no
// drivers, and what it shows is exactly what the server says. SQLite has
// no server, so it is read by a throwaway Python container, the same one
// that takes its backups.
//
// Everything here is read-only by construction rather than by trust:
// queries run in read-only transactions (SQLite: query_only), and a query
// may not contain a second statement. That is a guard against accidents,
// not against an adversary — whoever can reach this can already deploy a
// container with the database's password in its environment.

const (
	// DefaultExploreLimit is how many rows a page holds when none is asked.
	DefaultExploreLimit = 50
	// MaxExploreLimit bounds a page, so one request cannot pull a table.
	MaxExploreLimit = 500
	// maxCell is where a single value is cut. A page is for looking, and a
	// 5 MB JSON column would make it unusable.
	maxCell = 4000
	// maxObjects bounds an overview — chiefly Redis, which can hold
	// millions of keys.
	maxObjects = 1000
	// queryTimeout stops a runaway query rather than letting it hold the
	// request open.
	queryTimeout = 15 * time.Second
)

// ExploreObject is one table, view, collection or key.
type ExploreObject struct {
	Name string `json:"name"`
	// Kind is table, view, collection, or the Redis type of a key.
	Kind string `json:"kind"`
	// Count is the rows, documents or elements it holds. For Postgres and
	// MySQL it is the planner's estimate, which is free; the exact count
	// comes with a page of rows.
	Count *int64 `json:"count,omitempty"`
	// Estimated marks Count as an estimate.
	Estimated bool `json:"estimated,omitempty"`
	// Size is its size on disk in bytes, where the engine reports one.
	Size int64 `json:"size,omitempty"`
	// TTL is the seconds until a Redis key expires; -1 when it does not.
	TTL *int64 `json:"ttl,omitempty"`
}

// ExploreOverview is what a database holds.
type ExploreOverview struct {
	Engine  store.DBEngine  `json:"engine"`
	Objects []ExploreObject `json:"objects"`
	// Size is the whole database's size in bytes, where known.
	Size int64 `json:"size,omitempty"`
	// Keys is the total number of keys, for Redis, where Objects is only
	// the first maxObjects of them matching the pattern.
	Keys *int64 `json:"keys,omitempty"`
	// Truncated is set when there was more than the overview shows.
	Truncated bool `json:"truncated,omitempty"`
	// Queryable says whether the database accepts a query (SQL engines).
	Queryable bool `json:"queryable"`
}

// ExploreResult is a page of rows. Each cell is JSON: a string, number,
// boolean, null, or a nested object for a document or a JSON column.
type ExploreResult struct {
	Columns []string            `json:"columns"`
	Rows    [][]json.RawMessage `json:"rows"`
	// Total is the number of rows the object holds, when known.
	Total *int64 `json:"total,omitempty"`
	// Offset echoes where this page starts.
	Offset int `json:"offset"`
	// More is set when rows follow this page.
	More bool `json:"more"`
	// Truncated lists nothing; it is set when a cell was cut to maxCell.
	Truncated bool  `json:"truncated,omitempty"`
	Elapsed   int64 `json:"elapsedMs"`
	// Kind is the Redis type of a key being shown, or the object's kind.
	Kind string `json:"kind,omitempty"`
}

// ErrNotQueryable is returned for a query against an engine that has no
// query language the explorer speaks.
var ErrNotQueryable = errors.New("only Postgres, MySQL and SQLite databases accept a query; browse the others")

// ExploreDatabase lists what a database holds. pattern filters Redis keys
// (glob syntax, default *); the other engines list everything.
func (e *Engine) ExploreDatabase(ctx context.Context, p *store.Project, d *store.Database, pattern string) (*ExploreOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+10*time.Second)
	defer cancel()
	ov := &ExploreOverview{Engine: d.Engine, Queryable: d.Engine.IsSQL()}
	var err error
	switch d.Engine {
	case store.EnginePostgres:
		err = e.pgOverview(ctx, p, d, ov)
	case store.EngineMySQL:
		err = e.mysqlOverview(ctx, p, d, ov)
	case store.EngineMongo:
		err = e.mongoOverview(ctx, p, d, ov)
	case store.EngineRedis:
		err = e.redisOverview(ctx, p, d, pattern, ov)
	case store.EngineSQLite:
		err = e.sqliteRun(ctx, p, d, map[string]any{"op": "overview"}, ov)
	default:
		err = fmt.Errorf("cannot explore a %s database", d.Engine)
	}
	if err != nil {
		return nil, err
	}
	if ov.Objects == nil {
		ov.Objects = []ExploreObject{}
	}
	sort.SliceStable(ov.Objects, func(i, j int) bool { return ov.Objects[i].Name < ov.Objects[j].Name })
	return ov, nil
}

// BrowseDatabase returns a page of one object. filter is a MongoDB query
// document (JSON) for a collection; it is ignored elsewhere.
func (e *Engine) BrowseDatabase(ctx context.Context, p *store.Project, d *store.Database, object, filter string, offset, limit int) (*ExploreResult, error) {
	if object == "" {
		return nil, errors.New("name a table, collection or key to browse")
	}
	offset, limit = clampPage(offset, limit)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+10*time.Second)
	defer cancel()
	start := time.Now()
	var (
		res *ExploreResult
		err error
	)
	switch d.Engine {
	case store.EnginePostgres:
		res, err = e.pgBrowse(ctx, p, d, object, offset, limit)
	case store.EngineMySQL:
		res, err = e.mysqlBrowse(ctx, p, d, object, offset, limit)
	case store.EngineMongo:
		res, err = e.mongoBrowse(ctx, p, d, object, filter, offset, limit)
	case store.EngineRedis:
		res, err = e.redisBrowse(ctx, p, d, object, offset, limit)
	case store.EngineSQLite:
		res = &ExploreResult{}
		err = e.sqliteRun(ctx, p, d, map[string]any{"op": "browse", "name": object, "offset": offset, "limit": limit}, res)
	default:
		err = fmt.Errorf("cannot browse a %s database", d.Engine)
	}
	if err != nil {
		return nil, err
	}
	res.Offset = offset
	res.finish(start)
	return res, nil
}

// QueryDatabase runs one read-only statement and returns up to limit rows.
func (e *Engine) QueryDatabase(ctx context.Context, p *store.Project, d *store.Database, query string, limit int) (*ExploreResult, error) {
	if !d.Engine.IsSQL() {
		return nil, ErrNotQueryable
	}
	q, err := singleStatement(query)
	if err != nil {
		return nil, err
	}
	_, limit = clampPage(0, limit)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+10*time.Second)
	defer cancel()
	start := time.Now()
	var res *ExploreResult
	switch d.Engine {
	case store.EnginePostgres:
		res, err = e.pgQuery(ctx, p, d, q, limit)
	case store.EngineMySQL:
		res, err = e.mysqlQuery(ctx, p, d, q, limit)
	case store.EngineSQLite:
		res = &ExploreResult{}
		err = e.sqliteRun(ctx, p, d, map[string]any{"op": "query", "sql": q, "limit": limit}, res)
	}
	if err != nil {
		return nil, err
	}
	res.finish(start)
	return res, nil
}

func clampPage(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = DefaultExploreLimit
	}
	if limit > MaxExploreLimit {
		limit = MaxExploreLimit
	}
	return offset, limit
}

// finish cuts oversized cells, fills in empty slices so the JSON has
// arrays rather than nulls, and records how long it took.
func (r *ExploreResult) finish(start time.Time) {
	if r.Columns == nil {
		r.Columns = []string{}
	}
	if r.Rows == nil {
		r.Rows = [][]json.RawMessage{}
	}
	for _, row := range r.Rows {
		for i, cell := range row {
			if len(cell) > maxCell {
				row[i] = truncatedCell(cell)
				r.Truncated = true
			}
		}
	}
	r.Elapsed = time.Since(start).Milliseconds()
}

// truncatedCell turns an oversized value into a string holding its start.
func truncatedCell(cell json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(cell, &s) != nil {
		s = string(cell)
	}
	r := []rune(s)
	if len(r) > maxCell {
		s = string(r[:maxCell]) + fmt.Sprintf("… (%d more characters)", len(r)-maxCell)
	}
	out, _ := json.Marshal(s)
	return out
}

// singleStatement trims a query and refuses one holding a second
// statement. A semicolon inside a string literal is refused too, which is
// a small price for not having to parse SQL to keep the guard honest.
func singleStatement(q string) (string, error) {
	q = strings.TrimSpace(q)
	for strings.HasSuffix(q, ";") {
		q = strings.TrimSpace(strings.TrimSuffix(q, ";"))
	}
	if q == "" {
		return "", errors.New("the query is empty")
	}
	if strings.Contains(q, ";") {
		return "", errors.New("one statement at a time: the query may not contain a semicolon")
	}
	return q, nil
}

// execDB runs a command in the database's container and returns stdout,
// turning a non-zero exit into an error carrying the client's message.
func (e *Engine) execDB(ctx context.Context, p *store.Project, d *store.Database, cmd, env []string) (string, error) {
	res, err := e.docker.ExecEnv(ctx, d.ContainerName(p.ID), cmd, env)
	if err != nil {
		return "", fmt.Errorf("reaching the %s container: %w", d.Engine, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return "", fmt.Errorf("%s", cleanClientError(msg))
	}
	return res.Stdout, nil
}

// cleanClientError keeps the part of a client's error a person can act on.
func cleanClientError(msg string) string {
	lines := strings.Split(msg, "\n")
	var keep []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "mysql: [Warning]") {
			continue
		}
		keep = append(keep, l)
	}
	out := strings.Join(keep, "\n")
	if len(out) > 1500 {
		out = out[:1500] + "…"
	}
	if out == "" {
		return "the query failed without saying why"
	}
	return out
}

// orderedRow decodes a JSON object keeping its keys in the order written,
// which is the column order. encoding/json's maps would lose it.
func orderedRow(line []byte) (keys []string, vals []json.RawMessage, err error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected an object, got %v", tok)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		vals = append(vals, v)
	}
	return keys, vals, nil
}

// rowsFromObjects turns JSON objects — Postgres rows or Mongo documents —
// into a grid whose columns are every key seen, in first-seen order.
func rowsFromObjects(lines [][]byte) ([]string, [][]json.RawMessage, error) {
	index := map[string]int{}
	var cols []string
	var parsed []map[string]json.RawMessage
	for _, line := range lines {
		keys, vals, err := orderedRow(line)
		if err != nil {
			return nil, nil, err
		}
		m := make(map[string]json.RawMessage, len(keys))
		for i, k := range keys {
			if _, seen := index[k]; !seen {
				index[k] = len(cols)
				cols = append(cols, k)
			}
			m[k] = vals[i]
		}
		parsed = append(parsed, m)
	}
	rows := make([][]json.RawMessage, 0, len(parsed))
	for _, m := range parsed {
		row := make([]json.RawMessage, len(cols))
		for i, c := range cols {
			if v, ok := m[c]; ok {
				row[i] = v
			} else {
				row[i] = json.RawMessage("null")
			}
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

func nonEmptyLines(s string) [][]byte {
	var out [][]byte
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			out = append(out, append([]byte(nil), line...))
		}
	}
	return out
}

// --- Postgres ---------------------------------------------------------------

func (e *Engine) psql(ctx context.Context, p *store.Project, d *store.Database, statements ...string) (string, error) {
	cmd := []string{"psql", "-U", d.User, "-d", d.DBName, "-qAt", "-v", "ON_ERROR_STOP=1", "-1",
		"-c", "SET TRANSACTION READ ONLY",
		"-c", fmt.Sprintf("SET LOCAL statement_timeout = '%ds'", int(queryTimeout.Seconds()))}
	for _, s := range statements {
		cmd = append(cmd, "-c", s)
	}
	return e.execDB(ctx, p, d, cmd, nil)
}

func (e *Engine) pgOverview(ctx context.Context, p *store.Project, d *store.Database, ov *ExploreOverview) error {
	out, err := e.psql(ctx, p, d,
		`SELECT json_build_object('size', pg_database_size(current_database()))::text`,
		`SELECT json_build_object(
		   'name', CASE WHEN n.nspname = 'public' THEN c.relname ELSE n.nspname || '.' || c.relname END,
		   'kind', CASE WHEN c.relkind IN ('v', 'm') THEN 'view' ELSE 'table' END,
		   'count', CASE WHEN c.relkind IN ('r', 'p') AND c.reltuples >= 0 THEN c.reltuples::bigint END,
		   'size', pg_total_relation_size(c.oid))::text
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relkind IN ('r', 'p', 'v', 'm')
		   AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp%'`)
	if err != nil {
		return err
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return errors.New("psql returned nothing")
	}
	var head struct{ Size int64 }
	_ = json.Unmarshal(lines[0], &head)
	ov.Size = head.Size
	for _, l := range lines[1:] {
		var o ExploreObject
		if err := json.Unmarshal(l, &o); err != nil {
			return fmt.Errorf("reading the table list: %w", err)
		}
		o.Estimated = o.Count != nil
		ov.Objects = append(ov.Objects, o)
	}
	return nil
}

// pgIdent quotes a table name the overview reported — "schema.table" or a
// bare name in public — as an identifier.
func pgIdent(name string) string {
	schema, rel := "public", name
	if i := strings.Index(name, "."); i > 0 {
		schema, rel = name[:i], name[i+1:]
	}
	q := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	return q(schema) + "." + q(rel)
}

func (e *Engine) pgBrowse(ctx context.Context, p *store.Project, d *store.Database, object string, offset, limit int) (*ExploreResult, error) {
	if err := e.knownObject(ctx, p, d, object); err != nil {
		return nil, err
	}
	id := pgIdent(object)
	lit := "'" + strings.ReplaceAll(id, "'", "''") + "'"
	out, err := e.psql(ctx, p, d,
		fmt.Sprintf(`SELECT json_build_object('columns', (SELECT json_agg(attname ORDER BY attnum) FROM pg_attribute WHERE attrelid = %s::regclass AND attnum > 0 AND NOT attisdropped), 'total', (SELECT count(*) FROM %s))::text`, lit, id),
		fmt.Sprintf(`SELECT row_to_json(t)::text FROM (SELECT * FROM %s OFFSET %d LIMIT %d) t`, id, offset, limit+1))
	if err != nil {
		return nil, err
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return nil, errors.New("psql returned nothing")
	}
	var head struct {
		Columns []string `json:"columns"`
		Total   int64    `json:"total"`
	}
	if err := json.Unmarshal(lines[0], &head); err != nil {
		return nil, fmt.Errorf("reading the table's columns: %w", err)
	}
	res := &ExploreResult{Total: &head.Total}
	rows := lines[1:]
	if len(rows) > limit {
		rows, res.More = rows[:limit], true
	}
	_, grid, err := rowsFromObjects(rows)
	if err != nil {
		return nil, err
	}
	res.Columns, res.Rows = head.Columns, grid
	if len(grid) > 0 && len(grid[0]) != len(head.Columns) {
		// Duplicate column names fold together in row_to_json; trust the
		// rows' own shape over the catalogue's in that case.
		res.Columns, res.Rows, _ = rowsFromObjects(rows)
	}
	return res, nil
}

func (e *Engine) pgQuery(ctx context.Context, p *store.Project, d *store.Database, q string, limit int) (*ExploreResult, error) {
	out, err := e.psql(ctx, p, d, fmt.Sprintf(`SELECT row_to_json(t)::text FROM (%s) t LIMIT %d`, q, limit+1))
	if err != nil {
		return nil, err
	}
	rows := nonEmptyLines(out)
	res := &ExploreResult{}
	if len(rows) > limit {
		rows, res.More = rows[:limit], true
	}
	res.Columns, res.Rows, err = rowsFromObjects(rows)
	return res, err
}

// --- MySQL ------------------------------------------------------------------

func (e *Engine) mysql(ctx context.Context, p *store.Project, d *store.Database, raw bool, sql string) (string, error) {
	cmd := []string{"mysql", "-uroot", "-D", d.DBName, "-B", "--default-character-set=utf8mb4"}
	if raw {
		cmd = append(cmd, "-N", "-r")
	}
	script := fmt.Sprintf("SET SESSION TRANSACTION READ ONLY; SET SESSION max_execution_time = %d; START TRANSACTION READ ONLY; %s",
		queryTimeout.Milliseconds(), sql)
	cmd = append(cmd, "-e", script)
	return e.execDB(ctx, p, d, cmd, []string{"MYSQL_PWD=" + d.Password})
}

func (e *Engine) mysqlOverview(ctx context.Context, p *store.Project, d *store.Database, ov *ExploreOverview) error {
	out, err := e.mysql(ctx, p, d, true,
		`SELECT JSON_OBJECT('name', table_name, 'kind', IF(table_type = 'VIEW', 'view', 'table'),
		   'count', IF(table_type = 'VIEW', NULL, table_rows),
		   'size', COALESCE(data_length, 0) + COALESCE(index_length, 0))
		 FROM information_schema.tables WHERE table_schema = DATABASE()`)
	if err != nil {
		return err
	}
	for _, l := range nonEmptyLines(out) {
		var o ExploreObject
		if err := json.Unmarshal(l, &o); err != nil {
			return fmt.Errorf("reading the table list: %w", err)
		}
		o.Estimated = o.Count != nil
		ov.Size += o.Size
		ov.Objects = append(ov.Objects, o)
	}
	return nil
}

func mysqlIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (e *Engine) mysqlBrowse(ctx context.Context, p *store.Project, d *store.Database, object string, offset, limit int) (*ExploreResult, error) {
	if err := e.knownObject(ctx, p, d, object); err != nil {
		return nil, err
	}
	id := mysqlIdent(object)
	countOut, err := e.mysql(ctx, p, d, true, "SELECT COUNT(*) FROM "+id)
	if err != nil {
		return nil, err
	}
	res, err := e.mysqlGrid(ctx, p, d, fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", id, limit+1, offset), limit)
	if err != nil {
		return nil, err
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(countOut), 10, 64); err == nil {
		res.Total = &n
	}
	if len(res.Columns) == 0 {
		// An empty table prints no header; ask the catalogue.
		lit := "'" + strings.ReplaceAll(object, "'", "''") + "'"
		out, err := e.mysql(ctx, p, d, true, "SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = "+lit+" ORDER BY ordinal_position")
		if err == nil {
			for _, l := range nonEmptyLines(out) {
				res.Columns = append(res.Columns, string(l))
			}
		}
	}
	return res, nil
}

func (e *Engine) mysqlQuery(ctx context.Context, p *store.Project, d *store.Database, q string, limit int) (*ExploreResult, error) {
	return e.mysqlGrid(ctx, p, d, fmt.Sprintf("SELECT * FROM (%s) AS publix_q LIMIT %d", q, limit+1), limit)
}

// mysqlGrid runs a query in batch mode and parses its tab-separated output.
func (e *Engine) mysqlGrid(ctx context.Context, p *store.Project, d *store.Database, sql string, limit int) (*ExploreResult, error) {
	out, err := e.mysql(ctx, p, d, false, sql)
	if err != nil {
		return nil, err
	}
	res := &ExploreResult{}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return res, nil
	}
	res.Columns = strings.Split(lines[0], "\t")
	for _, l := range lines[1:] {
		fields := strings.Split(l, "\t")
		row := make([]json.RawMessage, len(res.Columns))
		for i := range row {
			row[i] = json.RawMessage("null")
			if i < len(fields) && fields[i] != "NULL" {
				b, _ := json.Marshal(mysqlUnescape(fields[i]))
				row[i] = b
			}
		}
		res.Rows = append(res.Rows, row)
	}
	if len(res.Rows) > limit {
		res.Rows, res.More = res.Rows[:limit], true
	}
	return res, nil
}

// mysqlUnescape reverses batch mode's escaping of tabs, newlines and
// backslashes inside values.
func mysqlUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '0':
				b.WriteByte(0)
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// knownObject refuses a name the overview does not list, so a table name
// that reaches an SQL string has at least been seen in the catalogue.
func (e *Engine) knownObject(ctx context.Context, p *store.Project, d *store.Database, name string) error {
	ov := &ExploreOverview{}
	var err error
	if d.Engine == store.EnginePostgres {
		err = e.pgOverview(ctx, p, d, ov)
	} else {
		err = e.mysqlOverview(ctx, p, d, ov)
	}
	if err != nil {
		return err
	}
	for _, o := range ov.Objects {
		if o.Name == name {
			return nil
		}
	}
	return fmt.Errorf("%s has no table named %q", d.Name, name)
}

// --- MongoDB ----------------------------------------------------------------

func (e *Engine) mongosh(ctx context.Context, p *store.Project, d *store.Database, js string) (string, error) {
	return e.execDB(ctx, p, d, []string{"mongosh", "--quiet", "--norc",
		"-u", d.User, "-p", d.Password, "--authenticationDatabase", "admin",
		d.DBName, "--eval", js}, nil)
}

func (e *Engine) mongoOverview(ctx context.Context, p *store.Project, d *store.Database, ov *ExploreOverview) error {
	out, err := e.mongosh(ctx, p, d, `
const objects = db.getCollectionInfos({}).filter(c => !c.name.startsWith('system.')).map(c => {
  const view = c.type === 'view';
  let count = null, size = 0;
  if (!view) {
    count = db.getCollection(c.name).estimatedDocumentCount();
    try { size = db.getCollection(c.name).stats().totalSize || 0 } catch (e) {}
  }
  return { name: c.name, kind: view ? 'view' : 'collection', count, size };
});
print(EJSON.stringify({ objects, size: db.stats().totalSize || db.stats().dataSize || 0 }, { relaxed: true }));`)
	if err != nil {
		return err
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return errors.New("mongosh returned nothing")
	}
	var parsed struct {
		Objects []ExploreObject `json:"objects"`
		Size    float64         `json:"size"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &parsed); err != nil {
		return fmt.Errorf("reading the collection list: %w", err)
	}
	ov.Objects, ov.Size = parsed.Objects, int64(parsed.Size)
	return nil
}

func (e *Engine) mongoBrowse(ctx context.Context, p *store.Project, d *store.Database, object, filter string, offset, limit int) (*ExploreResult, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		filter = "{}"
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(filter), &probe); err != nil {
		return nil, fmt.Errorf("the filter must be a JSON object, like {\"status\": \"active\"}: %w", err)
	}
	name, _ := json.Marshal(object)
	f, _ := json.Marshal(filter)
	out, err := e.mongosh(ctx, p, d, fmt.Sprintf(`
const c = db.getCollection(%s);
const filter = EJSON.parse(%s);
const docs = c.find(filter).skip(%d).limit(%d).toArray();
const total = c.countDocuments(filter, { maxTimeMS: %d });
print(EJSON.stringify({ total }, { relaxed: true }));
for (const d of docs) print(EJSON.stringify(d, { relaxed: true }));`,
		name, f, offset, limit+1, queryTimeout.Milliseconds()))
	if err != nil {
		return nil, err
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return nil, errors.New("mongosh returned nothing")
	}
	var head struct{ Total int64 }
	if err := json.Unmarshal(lines[0], &head); err != nil {
		return nil, fmt.Errorf("reading the count: %w", err)
	}
	res := &ExploreResult{Total: &head.Total, Kind: "collection"}
	docs := lines[1:]
	if len(docs) > limit {
		docs, res.More = docs[:limit], true
	}
	res.Columns, res.Rows, err = rowsFromObjects(docs)
	return res, err
}

// --- Redis ------------------------------------------------------------------

// redisEval runs a Lua script, which is how a whole overview or a page of
// one key comes back as a single JSON document instead of redis-cli's
// line-oriented output, where a value holding a newline is ambiguous.
func (e *Engine) redisEval(ctx context.Context, p *store.Project, d *store.Database, script string, args ...string) ([]byte, error) {
	cmd := append([]string{"redis-cli", "--no-auth-warning", "--raw", "EVAL", script, "0"}, args...)
	out, err := e.execDB(ctx, p, d, cmd, []string{"REDISCLI_AUTH=" + d.Password})
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "ERR") || strings.HasPrefix(out, "(error)") {
		return nil, errors.New(out)
	}
	return []byte(out), nil
}

const redisOverviewLua = `
local pattern, max = ARGV[1], tonumber(ARGV[2])
local cursor, keys = "0", {}
repeat
  local r = redis.call("SCAN", cursor, "MATCH", pattern, "COUNT", 1000)
  cursor = r[1]
  for _, k in ipairs(r[2]) do
    if #keys < max then keys[#keys + 1] = k end
  end
until cursor == "0" or #keys >= max
local sizes = { string = "STRLEN", list = "LLEN", hash = "HLEN", set = "SCARD", zset = "ZCARD", stream = "XLEN" }
local out = {}
for i, k in ipairs(keys) do
  local t = redis.call("TYPE", k)["ok"]
  local n = cjson.null
  if sizes[t] then n = redis.call(sizes[t], k) end
  out[i] = { name = k, kind = t, count = n, ttl = redis.call("TTL", k) }
end
local used = string.match(redis.call("INFO", "memory"), "used_memory:(%d+)")
return cjson.encode({ objects = out, keys = redis.call("DBSIZE"), truncated = cursor ~= "0", size = tonumber(used) or 0 })`

func (e *Engine) redisOverview(ctx context.Context, p *store.Project, d *store.Database, pattern string, ov *ExploreOverview) error {
	if pattern == "" {
		pattern = "*"
	}
	out, err := e.redisEval(ctx, p, d, redisOverviewLua, pattern, strconv.Itoa(maxObjects))
	if err != nil {
		return err
	}
	var parsed struct {
		Objects   json.RawMessage `json:"objects"`
		Keys      int64           `json:"keys"`
		Truncated bool            `json:"truncated"`
		Size      int64           `json:"size"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return fmt.Errorf("reading the key list: %w", err)
	}
	// cjson encodes an empty table as {}, not [].
	if len(parsed.Objects) > 0 && parsed.Objects[0] == '[' {
		if err := json.Unmarshal(parsed.Objects, &ov.Objects); err != nil {
			return fmt.Errorf("reading the key list: %w", err)
		}
	}
	ov.Keys, ov.Truncated, ov.Size = &parsed.Keys, parsed.Truncated, parsed.Size
	return nil
}

const redisBrowseLua = `
local key, offset, limit = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3])
local t = redis.call("TYPE", key)["ok"]
local cols, rows, total = {}, {}, 0
local function scan(cmd, pairs_)
  local cursor, items = "0", {}
  repeat
    local r = redis.call(cmd, key, cursor, "COUNT", 500)
    cursor = r[1]
    for _, v in ipairs(r[2]) do items[#items + 1] = v end
  until cursor == "0" or #items >= (offset + limit + 1) * pairs_
  return items
end
if t == "none" then
  return cjson.encode({ kind = "none" })
elseif t == "string" then
  cols = { "value" }
  total = 1
  if offset == 0 then rows[1] = { redis.call("GETRANGE", key, 0, 65535) } end
elseif t == "list" then
  cols = { "index", "value" }
  total = redis.call("LLEN", key)
  for i, v in ipairs(redis.call("LRANGE", key, offset, offset + limit)) do rows[i] = { offset + i - 1, v } end
elseif t == "hash" then
  cols = { "field", "value" }
  total = redis.call("HLEN", key)
  local items = scan("HSCAN", 2)
  for i = offset * 2 + 1, math.min(#items, (offset + limit + 1) * 2), 2 do rows[#rows + 1] = { items[i], items[i + 1] } end
elseif t == "set" then
  cols = { "member" }
  total = redis.call("SCARD", key)
  local items = scan("SSCAN", 1)
  for i = offset + 1, math.min(#items, offset + limit + 1) do rows[#rows + 1] = { items[i] } end
elseif t == "zset" then
  cols = { "member", "score" }
  total = redis.call("ZCARD", key)
  local items = redis.call("ZRANGE", key, offset, offset + limit, "WITHSCORES")
  for i = 1, #items, 2 do rows[#rows + 1] = { items[i], tonumber(items[i + 1]) } end
elseif t == "stream" then
  cols = { "id", "fields" }
  total = redis.call("XLEN", key)
  local items = redis.call("XRANGE", key, "-", "+", "COUNT", offset + limit + 1)
  for i = offset + 1, #items do
    local f = {}
    local kv = items[i][2]
    for j = 1, #kv, 2 do f[kv[j]] = kv[j + 1] end
    rows[#rows + 1] = { items[i][1], f }
  end
else
  return cjson.encode({ kind = t })
end
return cjson.encode({ kind = t, columns = cols, rows = rows, total = total, ttl = redis.call("TTL", key) })`

func (e *Engine) redisBrowse(ctx context.Context, p *store.Project, d *store.Database, key string, offset, limit int) (*ExploreResult, error) {
	out, err := e.redisEval(ctx, p, d, redisBrowseLua, key, strconv.Itoa(offset), strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Kind    string          `json:"kind"`
		Columns json.RawMessage `json:"columns"`
		Rows    json.RawMessage `json:"rows"`
		Total   int64           `json:"total"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("reading the key: %w", err)
	}
	switch parsed.Kind {
	case "none":
		return nil, fmt.Errorf("%s has no key named %q", d.Name, key)
	case "string", "list", "hash", "set", "zset", "stream":
	default:
		return nil, fmt.Errorf("%q is a %s, which the explorer cannot show", key, parsed.Kind)
	}
	res := &ExploreResult{Kind: parsed.Kind, Total: &parsed.Total}
	_ = json.Unmarshal(parsed.Columns, &res.Columns)
	if len(parsed.Rows) > 0 && parsed.Rows[0] == '[' {
		if err := json.Unmarshal(parsed.Rows, &res.Rows); err != nil {
			return nil, fmt.Errorf("reading the key: %w", err)
		}
	}
	if len(res.Rows) > limit {
		res.Rows, res.More = res.Rows[:limit], true
	}
	return res, nil
}

// --- SQLite -----------------------------------------------------------------

// sqliteExplore runs in sqliteSnapshotImage with the database's volume at
// /db. It reads its request from PUBLIX_EXPLORE and prints one JSON
// document. The file is opened read-only, and query_only refuses writes
// even from a statement that tries.
const sqliteExplore = `
import glob, json, os, sqlite3, sys
req = json.loads(os.environ["PUBLIX_EXPLORE"])
path = "/db/" + req["file"]
if not os.path.exists(path):
    found = sorted(glob.glob("/db/*.db") + glob.glob("/db/*.sqlite") + glob.glob("/db/*.sqlite3"))
    if not found:
        print(json.dumps({"error": "the database file does not exist yet — the app has not created it"}))
        sys.exit(0)
    path = found[0]
con = sqlite3.connect("file:" + path + "?mode=ro", uri=True, timeout=10)
con.execute("pragma query_only = 1")

def cell(v):
    if isinstance(v, bytes):
        return "<%d bytes of binary>" % len(v)
    return v

def ident(name):
    return '"' + name.replace('"', '""') + '"'

def objects():
    return con.execute("select name, type from sqlite_master where type in ('table', 'view') and name not like 'sqlite_%' order by name").fetchall()

op = req["op"]
if op == "overview":
    out = []
    for name, kind in objects():
        n = None
        if kind == "table":
            n = con.execute("select count(*) from " + ident(name)).fetchone()[0]
        out.append({"name": name, "kind": kind, "count": n})
    size = sum(os.path.getsize(f) for f in glob.glob(path + "*"))
    print(json.dumps({"objects": out, "size": size, "queryable": True}))
elif op == "browse":
    names = [n for n, _ in objects()]
    if req["name"] not in names:
        print(json.dumps({"error": "there is no table named %r" % req["name"]}))
        sys.exit(0)
    total = con.execute("select count(*) from " + ident(req["name"])).fetchone()[0]
    cur = con.execute("select * from %s limit ? offset ?" % ident(req["name"]), (req["limit"] + 1, req["offset"]))
    cols = [c[0] for c in cur.description]
    rows = [[cell(v) for v in r] for r in cur.fetchall()]
    more = len(rows) > req["limit"]
    print(json.dumps({"columns": cols, "rows": rows[:req["limit"]], "total": total, "more": more}, default=str))
else:
    try:
        cur = con.execute("select * from (%s) limit ?" % req["sql"], (req["limit"] + 1,))
    except sqlite3.Error as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(0)
    cols = [c[0] for c in cur.description] if cur.description else []
    rows = [[cell(v) for v in r] for r in cur.fetchall()]
    more = len(rows) > req["limit"]
    print(json.dumps({"columns": cols, "rows": rows[:req["limit"]], "more": more}, default=str))
`

// sqliteRun asks the explorer script for something and decodes the reply
// into out.
func (e *Engine) sqliteRun(ctx context.Context, p *store.Project, d *store.Database, req map[string]any, out any) error {
	req["file"] = d.Name + ".db"
	body, _ := json.Marshal(req)
	// The volume is mounted writable because reading a database in WAL
	// mode has to touch its -shm file; the connection itself is read-only.
	stdout, err := e.runHelper(ctx, p, d, sqliteSnapshotImage,
		[]string{"python3", "-c", sqliteExplore}, []string{"PUBLIX_EXPLORE=" + string(body)},
		[]string{d.VolumeName(p.ID) + ":/db"})
	if err != nil {
		return err
	}
	lines := nonEmptyLines(stdout)
	if len(lines) == 0 {
		return errors.New("the SQLite reader printed nothing")
	}
	last := lines[len(lines)-1]
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(last, &failure) == nil && failure.Error != "" {
		return errors.New(failure.Error)
	}
	return json.NewDecoder(io.LimitReader(bytes.NewReader(last), 64<<20)).Decode(out)
}
