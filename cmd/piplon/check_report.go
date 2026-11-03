package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// checkSchema names the JSON document of piplon check. Fields may be added,
// never renamed, removed or retyped; a breaking change is a new schema.
const checkSchema = "piplon.check.v1"

// checkReport is one judged pass.
type checkReport struct {
	opts              checkOptions
	scenario          string
	started, finished time.Time
	results           []targetResult // in config order
	hard              int            // targets that failed hard
	soft              int            // targets that crossed --max-ttft or --require-cache
}

// judge sets each target's failures. A hard failure is the only one
// reported for its target: a TTFT or cache figure means nothing then.
func (r *checkReport) judge() {
	for i := range r.results {
		t := &r.results[i]
		v := t.verdict
		switch {
		case v.State.Hard():
			cause, _, _ := strings.Cut(v.Cause, ": ")
			t.failures = []string{fmt.Sprintf("%s: %s", v.State, cause)}
			r.hard++
			continue
		case r.opts.maxTTFT > 0 && t.ttft > r.opts.maxTTFT:
			t.failures = append(t.failures, fmt.Sprintf("TTFT %d ms above %d ms", t.ttft.Milliseconds(), r.opts.maxTTFT.Milliseconds()))
		}
		switch {
		case !r.opts.requireCache || v.Cache == "hit":
		case v.Cache == "miss":
			t.failures = append(t.failures, "cache not read")
		default:
			t.failures = append(t.failures, "no turn expected to read the cache")
		}
		if len(t.failures) > 0 {
			r.soft++
		}
	}
}

func (r *checkReport) passed() bool { return r.hard <= r.opts.maxErrors && r.soft == 0 }

func (r *checkReport) exitCode() int {
	if r.passed() {
		return checkPassed
	}
	return checkFailed
}

func (r *checkReport) costUSD() float64 {
	var usd float64
	for _, t := range r.results {
		usd += t.costUSD
	}
	return usd
}

// text writes the human result: a header with the thresholds, one row per
// target and a summary line. Colours only when w is a terminal.
func (r *checkReport) text(w io.Writer) error {
	re := lipgloss.NewRenderer(w)
	colour := func(c string) lipgloss.Style { return re.NewStyle().Foreground(lipgloss.Color(c)) }
	green, yellow, red := colour("2"), colour("3"), colour("1")

	header := []string{"piplon check", r.scenario, plural(len(r.results), "target")}
	if r.opts.maxTTFT > 0 {
		header = append(header, "max TTFT "+short(r.opts.maxTTFT))
	}
	if r.opts.requireCache {
		header = append(header, "cache required")
	}
	if r.opts.maxErrors > 0 {
		header = append(header, fmt.Sprintf("%s tolerated", plural(r.opts.maxErrors, "hard failure")))
	}
	if r.opts.retries > 0 {
		header = append(header, plural(r.opts.retries, "retry"))
	}

	rows := make([][]string, len(r.results))
	styles := make([]lipgloss.Style, len(r.results))
	for i, t := range r.results {
		result, note := "pass", strings.Join(t.failures, "; ")
		if len(t.failures) > 0 {
			result = "FAIL"
		}
		if t.verdict.State.Hard() {
			note = t.verdict.Detail
			if c, _, _ := strings.Cut(t.verdict.Cause, ": "); c != "" {
				note = c + " · " + note
			}
		}
		if t.attempts > 1 {
			note += fmt.Sprintf(" (%d attempts)", t.attempts)
		}
		styles[i] = green
		switch {
		case t.verdict.State.Hard():
			styles[i] = red
		case len(t.failures) > 0:
			styles[i] = yellow
		}
		rows[i] = []string{t.target.Name, result, string(t.verdict.State), ms(t.ttft), cacheOf(t), fmt.Sprintf("%.4f USD", t.costUSD), strings.TrimSpace(note)}
	}
	tbl := table.New().Headers("TARGET", "RESULT", "STATE", "TTFT p50", "CACHE", "COST", "NOTE").Rows(rows...).
		Border(lipgloss.HiddenBorder()).BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).BorderHeader(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := re.NewStyle().PaddingRight(1)
			if row >= 0 && (col == 1 || col == 2) {
				return styles[row].PaddingRight(1)
			}
			return s
		})
	lines := strings.Split(tbl.Render(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}

	failed := 0
	for _, t := range r.results {
		if len(t.failures) > 0 {
			failed++
		}
	}
	summary := fmt.Sprintf("%s passed", plural(len(r.results), "target"))
	if failed > 0 {
		summary = fmt.Sprintf("%d of %s failed", failed, plural(len(r.results), "target"))
		if r.passed() {
			summary += ", within --max-errors"
		}
	}
	summary += fmt.Sprintf(" · %.4f USD · exit %d", r.costUSD(), r.exitCode())
	_, err := fmt.Fprintf(w, "%s\n\n%s\n\n%s\n", strings.Join(header, " · "), strings.TrimRight(strings.Join(lines, "\n"), "\n"), summary)
	return err
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(word, "y"))
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// cacheOf is the cache column: hit, miss, or - when no cache turn completed.
func cacheOf(t targetResult) string {
	if t.verdict.Cache == "" {
		return "-"
	}
	return t.verdict.Cache
}

// The JSON document. Every field is present, null when it has no value;
// times are RFC 3339 in UTC, durations seconds.
type checkDoc struct {
	Schema     string           `json:"schema"`
	Scenario   string           `json:"scenario"`
	StartedAt  string           `json:"started_at"`
	FinishedAt string           `json:"finished_at"`
	Thresholds checkThresholds  `json:"thresholds"`
	Passed     bool             `json:"passed"`
	ExitCode   int              `json:"exit_code"`
	CostUSD    float64          `json:"cost_usd"`
	Targets    []checkTargetDoc `json:"targets"`
}

type checkThresholds struct {
	MaxTTFTSeconds *float64 `json:"max_ttft_seconds"`
	MaxErrors      int      `json:"max_errors"`
	RequireCache   bool     `json:"require_cache"`
	Retries        int      `json:"retries"`
}

type checkTargetDoc struct {
	Name        string         `json:"name"`
	Format      string         `json:"format"`
	Model       string         `json:"model"`
	Passed      bool           `json:"passed"`
	State       string         `json:"state"`
	Cause       *string        `json:"cause"`
	Detail      *string        `json:"detail"`
	TTFTSeconds *float64       `json:"ttft_seconds"`
	Cache       *string        `json:"cache"`
	CostUSD     float64        `json:"cost_usd"`
	Attempts    int            `json:"attempts"`
	Failures    []string       `json:"failures"`
	Turns       []checkTurnDoc `json:"turns"`
}

type checkTurnDoc struct {
	Turn             int      `json:"turn"`
	HTTPStatus       *int     `json:"http_status"`
	TTFTSeconds      *float64 `json:"ttft_seconds"`
	TotalSeconds     float64  `json:"total_seconds"`
	InputTokens      int      `json:"input_tokens"`
	OutputTokens     int      `json:"output_tokens"`
	CacheReadTokens  int      `json:"cache_read_tokens"`
	CacheWriteTokens int      `json:"cache_write_tokens"`
	StreamCut        bool     `json:"stream_cut"`
	ExpectOK         bool     `json:"expect_ok"`
	Error            *string  `json:"error"`
}

// checkErrorDoc is the document of a pass that could not be judged.
type checkErrorDoc struct {
	Schema   string `json:"schema"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
}

func (r *checkReport) doc() checkDoc {
	d := checkDoc{Schema: checkSchema, Scenario: r.scenario,
		StartedAt: r.started.UTC().Format(time.RFC3339), FinishedAt: r.finished.UTC().Format(time.RFC3339),
		Thresholds: checkThresholds{MaxErrors: r.opts.maxErrors, RequireCache: r.opts.requireCache, Retries: r.opts.retries},
		Passed:     r.passed(), ExitCode: r.exitCode(), CostUSD: roundUSD(r.costUSD()), Targets: []checkTargetDoc{}}
	if r.opts.maxTTFT > 0 {
		d.Thresholds.MaxTTFTSeconds = seconds(r.opts.maxTTFT)
	}
	for _, t := range r.results {
		td := checkTargetDoc{Name: t.target.Name, Format: t.target.Format, Model: t.target.Model,
			Passed: len(t.failures) == 0, State: string(t.verdict.State), Cause: orNull(t.verdict.Cause),
			Detail: orNull(t.verdict.Detail), CostUSD: roundUSD(t.costUSD), Attempts: t.attempts,
			Failures: append([]string{}, t.failures...), Turns: []checkTurnDoc{}}
		if t.ttft > 0 {
			td.TTFTSeconds = seconds(t.ttft)
		}
		if c := cacheOf(t); c != "-" {
			td.Cache = &c
		}
		for i, turn := range t.turns {
			tu := checkTurnDoc{Turn: i + 1, TotalSeconds: *seconds(turn.Total), InputTokens: turn.InputTokens,
				OutputTokens: turn.OutputTokens, CacheReadTokens: turn.CacheReadTokens, CacheWriteTokens: turn.CacheWriteTokens,
				StreamCut: turn.StreamCut, ExpectOK: turn.ExpectOK, Error: orNull(turn.Error)}
			if turn.HTTPStatus != 0 {
				status := turn.HTTPStatus
				tu.HTTPStatus = &status
			}
			if turn.TTFT > 0 {
				tu.TTFTSeconds = seconds(turn.TTFT)
			}
			td.Turns = append(td.Turns, tu)
		}
		d.Targets = append(d.Targets, td)
	}
	return d
}

func writeCheckJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// seconds is d in seconds to the millisecond.
func seconds(d time.Duration) *float64 {
	s := float64(d.Milliseconds()) / 1e3
	return &s
}

func roundUSD(usd float64) float64 { return math.Round(usd*1e6) / 1e6 }

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
