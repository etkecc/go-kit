package crontab

import (
	"strings"
	"testing"
	"time"
)

func TestParseAccept(t *testing.T) {
	specs := []string{
		"* * * * *",
		"0 0 1 1 0",
		"1,2,3 * * * *",
		"0-30 * * * *",
		"*/15 * * * *",
		"1-59/2 * * * *",
		"0 0 * * 7",    // dow 7 == Sunday
		"5/15 * * * *", // bare-value step == 5-59/15
		"0 0 30 2 *",   // Feb 30: parses fine, simply never fires
		"1  0 1 * 1",   // extra whitespace collapses
	}
	for _, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			if _, err := parse(spec); err != nil {
				t.Fatalf("parse(%q) unexpected error: %v", spec, err)
			}
		})
	}
}

func TestParseReject(t *testing.T) {
	cases := []struct {
		spec    string
		wantSub string // the error must name the failing field (or the arity phrase)
	}{
		{"* * * *", "expected 5 fields"},
		{"* * * * * *", "expected 5 fields"},
		{"", "expected 5 fields"},
		{"60 * * * *", "minute"},
		{"* 24 * * *", "hour"},
		{"* * 32 * *", "day-of-month"},
		{"* * 0 * *", "day-of-month"},
		{"* * * 13 *", "month"},
		{"* * * 0 *", "month"},
		{"* * * * 8", "day-of-week"},
		{"*/0 * * * *", "minute"}, // the infinite-loop step, rejected
		{"* */0 * * *", "hour"},
		{"10-5 * * * *", "minute"}, // reversed range
		{"abc * * * *", "minute"},  // non-numeric
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			_, err := parse(tc.spec)
			if err == nil {
				t.Fatalf("parse(%q) expected error, got nil", tc.spec)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("parse(%q) error %q, want it to mention %q", tc.spec, err, tc.wantSub)
			}
		})
	}
}

func TestMatchMinuteForms(t *testing.T) {
	// hour/dom/month/dow are all "*" in these specs, so match depends only on the minute.
	at := func(t *testing.T, spec string, minute int) bool {
		s, err := parse(spec)
		if err != nil {
			t.Fatalf("parse(%q): %v", spec, err)
		}
		return s.match(time.Date(2026, 8, 3, 0, minute, 0, 0, time.UTC))
	}
	cases := []struct {
		spec string
		hit  []int
		miss []int
	}{
		{"*/15 * * * *", []int{0, 15, 30, 45}, []int{7, 14, 16}},
		{"1-59/2 * * * *", []int{1, 3, 59}, []int{0, 2, 58}},
		{"1,2,3 * * * *", []int{1, 2, 3}, []int{0, 4}},
		{"0-30 * * * *", []int{0, 15, 30}, []int{31, 45}},
		{"5/15 * * * *", []int{5, 20, 35, 50}, []int{0, 4, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			for _, m := range tc.hit {
				if !at(t, tc.spec, m) {
					t.Errorf("%q should match minute %d", tc.spec, m)
				}
			}
			for _, m := range tc.miss {
				if at(t, tc.spec, m) {
					t.Errorf("%q should not match minute %d", tc.spec, m)
				}
			}
		})
	}
}

// TestVixieUnion is the tripwire for the day-of-month / day-of-week union. "1 0 1 * 1"
// means 00:01 on the 1st of any month OR any Monday. A matcher that ANDs the two day
// fields fires only when the 1st happens to be a Monday, misses both arms the rest of
// the time, and does it in total silence. The two "must match" assertions below are
// exactly the cases an AND breaks.
func TestVixieUnion(t *testing.T) {
	s, err := parse("1 0 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	firstNotMonday := time.Date(2026, 8, 1, 0, 1, 0, 0, time.UTC) // Saturday the 1st
	mondayNot1st := time.Date(2026, 8, 3, 0, 1, 0, 0, time.UTC)   // Monday the 3rd
	ordinary := time.Date(2026, 8, 4, 0, 1, 0, 0, time.UTC)       // Tuesday the 4th

	// Guard against a mis-picked calendar date silently making the test pass.
	if firstNotMonday.Weekday() == time.Monday || firstNotMonday.Day() != 1 {
		t.Fatalf("setup: firstNotMonday is %s day %d", firstNotMonday.Weekday(), firstNotMonday.Day())
	}
	if mondayNot1st.Weekday() != time.Monday || mondayNot1st.Day() == 1 {
		t.Fatalf("setup: mondayNot1st is %s day %d", mondayNot1st.Weekday(), mondayNot1st.Day())
	}
	if ordinary.Weekday() == time.Monday || ordinary.Day() == 1 {
		t.Fatalf("setup: ordinary is %s day %d", ordinary.Weekday(), ordinary.Day())
	}

	if !s.match(firstNotMonday) {
		t.Error("must match the 1st even when it is not a Monday (dom arm of the union)")
	}
	if !s.match(mondayNot1st) {
		t.Error("must match Monday even when it is not the 1st (dow arm of the union)")
	}
	if s.match(ordinary) {
		t.Error("must not match a day that is neither the 1st nor a Monday")
	}
}

// TestMatchDayStar covers the intersection path: when one day field is "*", the star
// field reduces to all-true and the match falls through to the other field alone.
func TestMatchDayStar(t *testing.T) {
	domOnly, err := parse("0 0 1 * *") // 1st of month, any weekday
	if err != nil {
		t.Fatal(err)
	}
	if !domOnly.match(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("0 0 1 * * should match the 1st regardless of weekday")
	}
	if domOnly.match(time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)) {
		t.Error("0 0 1 * * should not match the 2nd")
	}

	dowOnly, err := parse("0 0 * * 1") // every Monday, any day-of-month
	if err != nil {
		t.Fatal(err)
	}
	if !dowOnly.match(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)) {
		t.Error("0 0 * * 1 should match a Monday")
	}
	if dowOnly.match(time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)) {
		t.Error("0 0 * * 1 should not match a Tuesday")
	}
}

// TestMatchSunday covers dow normalization: both 0 and 7 mean Sunday.
func TestMatchSunday(t *testing.T) {
	sunday := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("setup: %s is not Sunday", sunday.Weekday())
	}
	for _, spec := range []string{"0 0 * * 0", "0 0 * * 7"} {
		s, err := parse(spec)
		if err != nil {
			t.Fatalf("parse(%q): %v", spec, err)
		}
		if !s.match(sunday) {
			t.Errorf("%q should match Sunday", spec)
		}
	}
}
