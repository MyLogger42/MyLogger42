package store

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

func TestQuoteLogsQL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`plain`, `"plain"`},
		{``, `""`},
		{`with space`, `"with space"`},
		// The security-relevant ones: a bare quote must not be able to
		// terminate the literal and let the rest of the input become
		// query syntax.
		{`a"b`, `"a\"b"`},
		{`" OR project_id:="other`, `"\" OR project_id:=\"other"`},
		{`back\slash`, `"back\\slash"`},
		// Backslash-quote must escape to \\\" — escaping the quote first
		// and then the backslash would corrupt it.
		{`\"`, `"\\\""`},
	}
	for _, tc := range cases {
		if got := quoteLogsQL(tc.in); got != tc.want {
			t.Errorf("quoteLogsQL(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// A search term carrying a double quote must stay inside its own filter.
// If it escapes, it widens the query past the project_id predicate and
// leaks other projects' logs.
func TestBuildLogsQLSearchCannotEscapeProjectFilter(t *testing.T) {
	f := domain.QueryFilter{Search: `" OR project_id:="victim`}
	got := buildLogsQL("proj_mine", f)

	if !strings.HasPrefix(got, `project_id:="proj_mine" AND `) {
		t.Fatalf("project filter no longer leads the query: %s", got)
	}
	// Exactly one unescaped project_id predicate should exist.
	if n := strings.Count(got, `project_id:="`); n != 1 {
		t.Fatalf("query contains %d project_id predicates, want 1: %s", n, got)
	}
	if strings.Contains(got, `project_id:="victim"`) {
		t.Fatalf("injected project filter survived: %s", got)
	}
}

func TestBuildLogsQL(t *testing.T) {
	cases := []struct {
		name   string
		filter domain.QueryFilter
		want   string
	}{
		{
			"ProjectOnly",
			domain.QueryFilter{},
			`project_id:="proj_1"`,
		},
		{
			"AllFields",
			domain.QueryFilter{Service: "signup", Level: domain.LevelError, Search: "db timeout"},
			`project_id:="proj_1" AND service:="signup" AND level:="Error" AND _msg:i("db timeout")`,
		},
		{
			"ServiceOnly",
			domain.QueryFilter{Service: "api"},
			`project_id:="proj_1" AND service:="api"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildLogsQL("proj_1", tc.filter); got != tc.want {
				t.Errorf("buildLogsQL()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// The time range must actually reach the server. It travels as
// start/end form params rather than a _time: filter, so assert on the
// request rather than on the query string.
func TestQuerySendsTimeRangeAndPagination(t *testing.T) {
	var gotForm url.Values
	var queries []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		queries = append(queries, r.Form.Get("query"))
		if gotForm == nil {
			gotForm = r.Form
		}
		// First call is the page, second is the stats count.
		if strings.Contains(r.Form.Get("query"), "stats count()") {
			_, _ = io.WriteString(w, `{"total":"7"}`+"\n")
			return
		}
		_, _ = io.WriteString(w, "")
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	from := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	_, total, err := s.Query(context.Background(), "proj_1", domain.QueryFilter{
		From: &from, To: &to, Limit: 25, Offset: 50, Sort: domain.SortAsc,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if got := gotForm.Get("start"); got != "2026-08-31T10:00:00Z" {
		t.Errorf("start = %q, want 2026-08-31T10:00:00Z", got)
	}
	if got := gotForm.Get("end"); got != "2026-08-31T12:00:00Z" {
		t.Errorf("end = %q, want 2026-08-31T12:00:00Z", got)
	}
	page := queries[0]
	for _, want := range []string{"| sort by (_time) asc", "| offset 50", "| limit 25"} {
		if !strings.Contains(page, want) {
			t.Errorf("page query missing %q: %s", want, page)
		}
	}
	// total must be the full match count from the stats call, not the
	// number of rows on this page.
	if total != 7 {
		t.Errorf("total = %d, want 7 (from stats count)", total)
	}
}

func TestQueryReturnsEmptySliceNotNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "")
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	items, total, err := s.Query(context.Background(), "proj_1", domain.QueryFilter{Limit: 50})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if items == nil {
		t.Fatal("Query returned nil slice; `items: null` breaks the SPA")
	}
	if len(items) != 0 || total != 0 {
		t.Fatalf("got %d items, total %d", len(items), total)
	}
}

// Insert writes metadata as a JSON string, because VictoriaLogs flattens
// nested objects into dotted field names and they would not round-trip.
func TestInsertSerialisesMetadataAsJSONString(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	ts := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	err = s.Insert(context.Background(), domain.LogEntry{
		ID: "log_1", ProjectID: "proj_1", Service: "api", Level: domain.LevelInfo,
		Message: "hello", Timestamp: ts, ReceivedAt: ts,
		Metadata: map[string]any{"nested": map[string]any{"host": "web-1"}},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &rec); err != nil {
		t.Fatalf("ingest body is not one JSON object per line: %v (%s)", err, body)
	}
	raw, ok := rec[fieldMetadata].(string)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON string", fieldMetadata, rec[fieldMetadata])
	}
	if raw != `{"nested":{"host":"web-1"}}` {
		t.Errorf("metadata = %s, want the nested object verbatim", raw)
	}
	if rec[fieldReceivedAt] != "2026-08-31T10:00:00Z" {
		t.Errorf("received_at = %v, want it to be sent", rec[fieldReceivedAt])
	}
}

// Whatever Insert writes, parseVLogsRecord must read back — including
// receivedAt, which the SPA renders directly.
func TestParseVLogsRecordRoundTrip(t *testing.T) {
	raw := map[string]any{
		fieldID:         "log_1",
		fieldProjectID:  "proj_1",
		fieldService:    "api",
		fieldLevel:      "Error",
		fieldMsg:        "db timeout",
		fieldTime:       "2026-08-31T10:00:00Z",
		fieldReceivedAt: "2026-08-31T10:00:01Z",
		fieldMetadata:   `{"attempt":3}`,
	}
	got := parseVLogsRecord(raw)

	if got.ID != "log_1" || got.ProjectID != "proj_1" || got.Service != "api" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.Level != domain.LevelError || got.Message != "db timeout" {
		t.Errorf("level/message wrong: %+v", got)
	}
	if !got.Timestamp.Equal(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Timestamp = %s", got.Timestamp)
	}
	if !got.ReceivedAt.Equal(time.Date(2026, 8, 31, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("ReceivedAt = %s, want it parsed (SPA renders it raw)", got.ReceivedAt)
	}
	if n, ok := got.Metadata["attempt"].(float64); !ok || n != 3 {
		t.Errorf("Metadata = %#v", got.Metadata)
	}
}

func TestParseVLogsRecordMetadataAlwaysObject(t *testing.T) {
	for _, tc := range []struct{ name, meta string }{
		{"Missing", ""},
		{"Corrupt", "{not json"},
		{"Null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseVLogsRecord(map[string]any{fieldMetadata: tc.meta})
			if got.Metadata == nil {
				t.Fatal("Metadata is nil; the SPA JSON.stringify's it directly")
			}
		})
	}
}

func TestVLogsGetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "")
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	if _, err := s.Get(context.Background(), "proj_1", "nope"); err != ErrNotFound {
		t.Fatalf("Get: want ErrNotFound, got %v", err)
	}
}

// A non-2xx from the server must surface as an error, not as an empty
// result set that looks like "no logs matched".
func TestVLogsQueryPropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cannot parse query", http.StatusBadRequest)
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	if _, _, err := s.Query(context.Background(), "proj_1", domain.QueryFilter{}); err == nil {
		t.Fatal("Query: want an error on HTTP 400, got nil")
	}
}

func TestVLogsPing(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if path != "/health" {
		t.Errorf("Ping hit %q, want /health", path)
	}
}

func TestVLogsPingFailsWhenServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("Ping: want an error on 503, got nil")
	}
}

// Retention is the engine's job here; the worker calls this per project
// and must get an honest zero rather than a lie or an error.
func TestVLogsDeleteIsNoop(t *testing.T) {
	s, err := OpenVictoriaLogs("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	n, err := s.DeleteOlderThanForProject(context.Background(), "proj_1", time.Now())
	if err != nil {
		t.Fatalf("DeleteOlderThanForProject: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted = %d, want 0", n)
	}
}

// TestVLogsStatsPipelinesAndFolding pins the two LogsQL pipelines Stats
// emits and the (bucket, level) → column folding, with no server needed.
// The conformance suite behind the `vlogs` tag proves the same behaviour
// against a real VictoriaLogs; this proves it on every `go test`.
func TestVLogsStatsPipelinesAndFolding(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		q := r.Form.Get("query")
		queries = append(queries, q)
		switch {
		case strings.Contains(q, "stats by (_time:"):
			// Newest bucket first (the pipeline sorts desc), two levels
			// inside one bucket — the folding case. Counts arrive as
			// strings.
			_, _ = io.WriteString(w, `{"_time":"2026-08-31T11:00:00Z","level":"Error","rows":"1"}`+"\n"+
				`{"_time":"2026-08-31T10:00:00Z","level":"Error","rows":"3"}`+"\n"+
				`{"_time":"2026-08-31T10:00:00Z","level":"Info","rows":"5"}`+"\n")
		case strings.Contains(q, "stats by (level)"):
			// Totals are their own uncapped aggregate — deliberately NOT
			// the sum of the (capped) series rows above, so this test
			// fails if the code goes back to summing them.
			_, _ = io.WriteString(w, `{"level":"Error","rows":"9"}`+"\n"+
				`{"level":"Info","rows":"7"}`+"\n")
		default:
			_, _ = io.WriteString(w,
				`{"service":"api","level":"Error","_msg":"redis timeout","rows":"3","first_seen":"2026-08-31T10:00:00Z","last_seen":"2026-08-31T10:40:00Z"}`+"\n")
		}
	}))
	defer srv.Close()

	s, err := OpenVictoriaLogs(srv.URL)
	if err != nil {
		t.Fatalf("OpenVictoriaLogs: %v", err)
	}
	got, err := s.Stats(context.Background(), "proj_1", domain.QueryFilter{Service: "api"}, domain.BucketHour)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if len(queries) != 3 {
		t.Fatalf("expected 3 pipelines, got %d: %v", len(queries), queries)
	}
	for _, want := range []string{
		`project_id:="proj_1"`, `service:="api"`,
		"stats by (_time:1h, level) count() rows",
		"| sort by (_time) desc | limit 5000",
	} {
		if !strings.Contains(queries[0], want) {
			t.Errorf("series pipeline missing %q: %s", want, queries[0])
		}
	}
	if !strings.Contains(queries[1], "stats by (level) count() rows") {
		t.Errorf("totals pipeline missing its stats pipe: %s", queries[1])
	}
	if strings.Contains(queries[1], "limit") {
		t.Errorf("totals pipeline must not be capped: %s", queries[1])
	}
	for _, want := range []string{
		"stats by (service, level, _msg) count() rows",
		"min(_time) first_seen", "max(_time) last_seen",
		"| sort by (rows desc) | limit 10",
	} {
		if !strings.Contains(queries[2], want) {
			t.Errorf("top pipeline missing %q: %s", want, queries[2])
		}
	}

	// Two rows for the same instant collapse into one column, and the
	// descending response comes back ascending for the chart.
	if len(got.Series) != 2 {
		t.Fatalf("Series = %+v, want 2 buckets", got.Series)
	}
	if want := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC); !got.Series[0].Start.Equal(want) {
		t.Errorf("Series[0].Start = %s, want %s (ascending)", got.Series[0].Start, want)
	}
	if got.Series[0].Counts[domain.LevelError] != 3 || got.Series[0].Counts[domain.LevelInfo] != 5 {
		t.Errorf("bucket 0 counts = %+v, want Error:3 Info:5", got.Series[0].Counts)
	}
	// From the totals pipeline, not the series sum (which would be 4/5).
	if got.Totals[domain.LevelError] != 9 || got.Totals[domain.LevelInfo] != 7 {
		t.Errorf("Totals = %+v, want Error:9 Info:7", got.Totals)
	}
	if len(got.TopMessages) != 1 || got.TopMessages[0].Count != 3 ||
		got.TopMessages[0].Message != "redis timeout" {
		t.Fatalf("TopMessages = %+v", got.TopMessages)
	}
	if want := time.Date(2026, 8, 31, 10, 40, 0, 0, time.UTC); !got.TopMessages[0].LastSeenAt.Equal(want) {
		t.Errorf("LastSeenAt = %s, want %s", got.TopMessages[0].LastSeenAt, want)
	}
}
