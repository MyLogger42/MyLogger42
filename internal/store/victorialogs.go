package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
)

// victoriaLogsStore implements LogStore against a VictoriaLogs server over
// its HTTP API. Only the log firehose lives here — users, projects and
// keys stay in SQLite (see OpenStores' hybrid branch).
//
// Two behaviours differ from the SQLite backend, both deliberate and both
// documented for operators:
//
//   - Search is a case-insensitive PHRASE match, not a substring match.
//     "imeou" no longer finds "timeout". This is what makes search hit the
//     index instead of scanning.
//   - DeleteOlderThanForProject is a no-op. VictoriaLogs exposes no delete
//     API; retention is the server's global -retentionPeriod flag, so
//     per-project retention_days is not enforced in this mode.
//
// One hazard operators need to know about, because we cannot detect it
// here: VictoriaLogs SILENTLY DROPS any entry whose _time is older than
// -retentionPeriod, and still answers the ingest request with HTTP 200.
// There is no per-request rejection count to inspect, so an ingest call
// that backfills old logs (or comes from a client with a skewed clock)
// returns 201 Created to the caller and stores nothing. The server logs
// a "skipping log entry with too small timestamp" warning. The SQLite
// backend accepts such entries, so this is a real behavioural difference
// — size -retentionPeriod to cover the oldest timestamp you ingest.
type victoriaLogsStore struct {
	baseURL string
	client  *http.Client
}

// defaultVLogsURL matches VictoriaLogs' default listen address.
const defaultVLogsURL = "http://localhost:9428"

// OpenVictoriaLogs builds a LogStore client. It performs no I/O — call
// Ping to confirm the server is actually reachable.
func OpenVictoriaLogs(baseURL string) (LogStore, error) {
	if baseURL == "" {
		baseURL = defaultVLogsURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("parse victorialogs url %q: %w", baseURL, err)
	}
	return &victoriaLogsStore{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Ingest is one request per log on the single-log path, so
			// connection reuse is the difference between a keep-alive
			// write and a fresh TCP+handshake per entry.
			Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

// --- field names ------------------------------------------------------
//
// _time and _msg are VictoriaLogs' reserved timestamp and message fields.
// The rest are ours. metadata is stored as a JSON *string* rather than a
// nested object because VictoriaLogs flattens nested JSON into dotted
// field names ({"a":{"b":1}} becomes a.b), which would not round-trip
// back into a map[string]any.
const (
	fieldTime       = "_time"
	fieldMsg        = "_msg"
	fieldProjectID  = "project_id"
	fieldService    = "service"
	fieldLevel      = "level"
	fieldID         = "id"
	fieldReceivedAt = "received_at"
	fieldMetadata   = "metadata_json"
)

func (v *victoriaLogsStore) Insert(ctx context.Context, entry domain.LogEntry) error {
	return v.InsertBatch(ctx, []domain.LogEntry{entry})
}

func (v *victoriaLogsStore) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // writes one object per line — exactly ndjson
	for _, e := range entries {
		meta := e.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("encode metadata: %w", err)
		}
		record := map[string]any{
			fieldTime:       e.Timestamp.UTC().Format(time.RFC3339Nano),
			fieldMsg:        e.Message,
			fieldProjectID:  e.ProjectID,
			fieldService:    e.Service,
			fieldLevel:      string(e.Level),
			fieldID:         e.ID,
			fieldReceivedAt: e.ReceivedAt.UTC().Format(time.RFC3339Nano),
			fieldMetadata:   string(metaJSON),
		}
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("marshal log entry: %w", err)
		}
	}

	q := url.Values{}
	q.Set("_time_field", fieldTime)
	q.Set("_msg_field", fieldMsg)
	q.Set("_stream_fields", fieldProjectID+","+fieldService)
	reqURL := v.baseURL + "/insert/jsonline?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/stream+json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vlogs ingest: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("vlogs ingest status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (v *victoriaLogsStore) Query(ctx context.Context, projectID string, f domain.QueryFilter) ([]domain.LogEntry, int, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	filter := buildLogsQL(projectID, f)
	pipeline := fmt.Sprintf("%s | sort by (%s) %s", filter, fieldTime, sortDirection(f.Sort))
	if offset > 0 {
		pipeline += " | offset " + strconv.Itoa(offset)
	}
	pipeline += " | limit " + strconv.Itoa(limit)

	entries, err := v.queryEntries(ctx, pipeline, f, limit)
	if err != nil {
		return nil, 0, err
	}
	// The SPA renders an exact "{total} entries match" and "Page X of Y",
	// so the count has to be the full match count, not len(entries).
	total, err := v.countMatches(ctx, filter, f)
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// queryEntries runs a LogsQL pipeline and decodes the ndjson response.
func (v *victoriaLogsStore) queryEntries(ctx context.Context, pipeline string, f domain.QueryFilter, capacity int) ([]domain.LogEntry, error) {
	resp, err := v.postQuery(ctx, "/select/logsql/query", pipeline, f)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	// Never nil: handleQuery has no nil guard, and `items: null` breaks
	// the SPA's items.map().
	out := make([]domain.LogEntry, 0, capacity)
	dec := json.NewDecoder(resp.Body)
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("vlogs decode: %w", err)
		}
		out = append(out, parseVLogsRecord(raw))
	}
	return out, nil
}

// countMatches returns the exact number of rows matching filter.
func (v *victoriaLogsStore) countMatches(ctx context.Context, filter string, f domain.QueryFilter) (int, error) {
	resp, err := v.postQuery(ctx, "/select/logsql/query", filter+" | stats count() total", f)
	if err != nil {
		return 0, err
	}
	defer drainClose(resp)

	dec := json.NewDecoder(resp.Body)
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				return 0, nil // no rows matched
			}
			return 0, fmt.Errorf("vlogs count decode: %w", err)
		}
		// stats results come back as strings.
		if s, ok := raw["total"].(string); ok {
			n, err := strconv.Atoi(s)
			if err != nil {
				return 0, fmt.Errorf("vlogs count %q: %w", s, err)
			}
			return n, nil
		}
		if n, ok := raw["total"].(float64); ok {
			return int(n), nil
		}
	}
}

// postQuery submits a LogsQL query as a form body. Form-encoding rather
// than URL interpolation keeps quoting bugs out of the request layer;
// LogsQL-level quoting is handled by quoteLogsQL.
//
// The time range travels as start/end params rather than a _time: filter
// — same effect, one less thing to escape.
func (v *victoriaLogsStore) postQuery(ctx context.Context, path, query string, f domain.QueryFilter) (*http.Response, error) {
	form := url.Values{}
	form.Set("query", query)
	if f.From != nil {
		form.Set("start", f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		form.Set("end", f.To.UTC().Format(time.RFC3339Nano))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlogs query: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		drainClose(resp)
		return nil, fmt.Errorf("vlogs query status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (v *victoriaLogsStore) Get(ctx context.Context, projectID, id string) (*domain.LogEntry, error) {
	pipeline := fmt.Sprintf(`%s:=%s AND %s:=%s | limit 1`,
		fieldProjectID, quoteLogsQL(projectID), fieldID, quoteLogsQL(id))

	entries, err := v.queryEntries(ctx, pipeline, domain.QueryFilter{}, 1)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNotFound
	}
	return &entries[0], nil
}

func (v *victoriaLogsStore) Sources(ctx context.Context, projectID string) ([]domain.Source, error) {
	pipeline := fmt.Sprintf(
		`%s:=%s | stats by (%s) count() total_entries, min(%s) first_seen, max(%s) last_seen`,
		fieldProjectID, quoteLogsQL(projectID), fieldService, fieldTime, fieldTime)

	resp, err := v.postQuery(ctx, "/select/logsql/query", pipeline, domain.QueryFilter{})
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)

	var out []domain.Source
	dec := json.NewDecoder(resp.Body)
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("vlogs sources decode: %w", err)
		}
		src := domain.Source{Name: stringField(raw, fieldService)}
		if n, err := strconv.ParseInt(stringField(raw, "total_entries"), 10, 64); err == nil {
			src.TotalEntries = n
		}
		src.FirstSeenAt = parseVLogsTime(stringField(raw, "first_seen"))
		src.LastSeenAt = parseVLogsTime(stringField(raw, "last_seen"))
		out = append(out, src)
	}
	// Match the SQLite backend's ORDER BY service ASC.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteOlderThanForProject is a no-op on VictoriaLogs.
//
// VictoriaLogs exposes no delete API, and its retention is a global
// server flag (-retentionPeriod), not a per-project or per-stream
// setting. The retention worker still calls this per project; reporting
// zero deletions is the honest answer. Per-project retention_days is
// therefore NOT enforced in this mode — the API surfaces that to the
// operator rather than silently ignoring the setting.
func (v *victoriaLogsStore) DeleteOlderThanForProject(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

func (v *victoriaLogsStore) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vlogs ping: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vlogs ping: status %d", resp.StatusCode)
	}
	return nil
}

// Close releases idle connections. There is no session to tear down.
func (v *victoriaLogsStore) Close() error {
	v.client.CloseIdleConnections()
	return nil
}

// --- LogsQL construction ----------------------------------------------

// buildLogsQL renders the filter half of a LogsQL query (no pipes).
//
// Every interpolated value goes through quoteLogsQL. That is a security
// boundary, not cosmetics: an unescaped double quote in a user-supplied
// search or service term would close its own filter and let the rest of
// the input escape the project_id predicate — cross-project disclosure.
//
// Exact filters (field:="value") are used for the structural fields.
// A phrase filter (field:"value") would match substrings of a project ID
// and could span projects.
func buildLogsQL(projectID string, f domain.QueryFilter) string {
	clauses := []string{fieldProjectID + ":=" + quoteLogsQL(projectID)}
	if f.Service != "" {
		clauses = append(clauses, fieldService+":="+quoteLogsQL(f.Service))
	}
	if f.Level != "" {
		clauses = append(clauses, fieldLevel+":="+quoteLogsQL(string(f.Level)))
	}
	if f.Search != "" {
		// Case-insensitive phrase match. This is the documented
		// divergence from SQLite's LIKE '%x%' substring behaviour.
		clauses = append(clauses, fmt.Sprintf("%s:i(%s)", fieldMsg, quoteLogsQL(f.Search)))
	}
	return strings.Join(clauses, " AND ")
}

// quoteLogsQL renders s as a double-quoted LogsQL string literal,
// escaping backslashes and double quotes. Order matters — backslashes
// first, or the escapes we add get escaped again.
func quoteLogsQL(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// sortDirection maps the API's sort param onto LogsQL's sort pipe.
// Default is desc, matching the SQLite backend and the SPA.
func sortDirection(s domain.SortOrder) string {
	if s == domain.SortAsc {
		return "asc"
	}
	return "desc"
}

// --- response parsing -------------------------------------------------

// parseVLogsRecord converts one response object back into a LogEntry.
// VictoriaLogs returns every field as a string.
func parseVLogsRecord(raw map[string]any) domain.LogEntry {
	e := domain.LogEntry{
		ID:         stringField(raw, fieldID),
		ProjectID:  stringField(raw, fieldProjectID),
		Service:    stringField(raw, fieldService),
		Level:      domain.LogLevel(stringField(raw, fieldLevel)),
		Message:    stringField(raw, fieldMsg),
		Timestamp:  parseVLogsTime(stringField(raw, fieldTime)),
		ReceivedAt: parseVLogsTime(stringField(raw, fieldReceivedAt)),
		Metadata:   map[string]any{},
	}
	if s := stringField(raw, fieldMetadata); s != "" {
		if err := json.Unmarshal([]byte(s), &e.Metadata); err != nil || e.Metadata == nil {
			// Mirror the SQLite backend: surface the bad row rather than
			// failing the whole listing.
			e.Metadata = map[string]any{"_decodeError": "invalid metadata json", "_raw": s}
		}
	}
	return e
}

// stringField reads a field as a string, tolerating numeric JSON values.
func stringField(raw map[string]any, name string) string {
	switch v := raw[name].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// parseVLogsTime parses a VictoriaLogs timestamp, returning the zero time
// on anything unrecognised.
func parseVLogsTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	// Stats aggregates over _time can come back as unix nanoseconds.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(0, n).UTC()
	}
	return time.Time{}
}

// drainClose consumes and closes a response body so the connection can be
// reused by the keep-alive pool.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}
