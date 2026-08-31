package domain

import (
	"errors"
	"time"
)

// LogStats is the aggregate projection behind GET /api/v1/stats. It backs
// three panels on the logs page from one round trip: a stacked
// level-over-time histogram (Series), the per-level count chips (Totals),
// and the "noisiest messages" list (TopMessages).
//
// Every field is derived under the SAME QueryFilter as the log list, so
// the panels always describe exactly the rows the user is looking at.
type LogStats struct {
	BucketSeconds int                `json:"bucketSeconds"`
	Series        []LevelBucket      `json:"series"`
	Totals        map[LogLevel]int64 `json:"totals"`
	TopMessages   []MessageGroup     `json:"topMessages"`
}

// LevelBucket is one histogram column: the bucket's start instant and the
// per-level counts inside it. Levels with no entries in the bucket are
// absent from the map rather than present with a zero — the chart treats
// missing as zero.
type LevelBucket struct {
	Start  time.Time          `json:"start"`
	Counts map[LogLevel]int64 `json:"counts"`
}

// MessageGroup is one repeated-message group: an exact (service, level,
// message) triple and how often it occurred in the filtered range. This is
// what surfaces "the same log 400 times in five minutes" — the list view
// can only ever show that as 400 rows.
type MessageGroup struct {
	Service     string    `json:"service"`
	Level       LogLevel  `json:"level"`
	Message     string    `json:"message"`
	Count       int64     `json:"count"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
}

// Bucket sizes are an allowlist, not free-form arithmetic: the value goes
// into a GROUP BY expression (SQLite) and a `_time:` pipe (VictoriaLogs),
// and an unbounded client-supplied divisor there is both a correctness and
// a cost hazard.
const (
	BucketMinute = 60
	BucketFive   = 300
	BucketHour   = 3600
	BucketDay    = 86400

	// DefaultBucketSeconds is what a request with no bucket param gets.
	DefaultBucketSeconds = BucketHour

	// TopMessagesLimit caps the repeated-message list. Ten is what the
	// panel renders without scrolling.
	TopMessagesLimit = 10
)

// StatsBuckets is the allowlist in ascending order, for clients that want
// to render the choices.
var StatsBuckets = []int{BucketMinute, BucketFive, BucketHour, BucketDay}

// ValidateBucketSeconds enforces the allowlist. Zero means "use the
// default" and is accepted.
func ValidateBucketSeconds(sec int) error {
	if sec == 0 {
		return nil
	}
	for _, b := range StatsBuckets {
		if b == sec {
			return nil
		}
	}
	return errors.New("bucket must be one of 60, 300, 3600, 86400")
}
