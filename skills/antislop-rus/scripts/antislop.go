// antislop is the deterministic gate of the antislop-rus skill: it checks Russian code-review text
// for slop, and with --source it checks that no fact of the source (English or Russian) was lost.
//
//	go run scripts/antislop.go [--json] [--source SRC] FILE|-
//	go run scripts/antislop.go [--json] --lookup TERM [TERM ...]
//
// Rules. Each finding has a rule, line:col (runes, 1-based), the matched text and a hint.
//
//	lexicon      hard  a row of the ru-non-approved table, Russian inflection included (stem + ending)
//	glued        hard  a Latin+Cyrillic token (guard'ом, prior-тред, CI-матрица); listed compounds from the ru-allowed table pass
//	glossary     hard  with an English SRC: SRC holds a glossary-en-ru term and FILE holds one of its "never" variants
//	fidelity     hard  with --source: a code span, file:line, path, number or hedge of SRC is missing in FILE,
//	                   or FILE adds certainty (kind "certainty") that SRC does not have
//	domain       soft  a ru-domain term (учётные «ноги», «узел» = хост) used in its domain sense
//	latin-prose  soft  a Latin word outside backticks that the ru-allowed table does not list
//	length       soft  a sentence over 25 words (an inline code span counts as 1 word)
//	facts        soft  a sentence with 3+ clause breaks (, ; spaced dash, «а», «но», «при этом»)
//
// The source language is detected from its prose letters: more Latin than Cyrillic means English.
// Code spans, fenced blocks and URLs are never checked for words. Exit status: 1 if any hard finding
// (or, with --lookup, no entry found), 0 otherwise, 2 on a usage or I/O error.
//
// Data. The tables in data/*.json are the source of truth; edit them directly. The data is embedded.
// Standard library only; works with `go run` from any directory.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

//go:embed data/*.json
var dataFS embed.FS

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `usage: go run <skill>/scripts/antislop.go [--json] [--source SRC] FILE|-
       go run <skill>/scripts/antislop.go [--json] --lookup TERM [TERM ...]`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("antislop", flag.ContinueOnError)
	fl.SetOutput(stderr)
	asJSON := fl.Bool("json", false, "print findings as JSON")
	source := fl.String("source", "", "original text (English or Russian); report its facts and hedges missing from FILE")
	lookup := fl.String("lookup", "", "print the glossary, hedge and lexicon entries for TERM (more terms may follow)")
	fl.Usage = func() {
		fmt.Fprintln(stderr, usage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	db, err := loadData()
	if err != nil {
		fmt.Fprintln(stderr, "antislop: data:", err)
		return 2
	}
	if *lookup != "" {
		return runLookup(db, append([]string{*lookup}, fl.Args()...), *asJSON, stdout)
	}
	if fl.NArg() != 1 {
		fl.Usage()
		return 2
	}
	name := fl.Arg(0)
	text, err := readInput(name, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "antislop:", err)
		return 2
	}
	srcText := ""
	if *source != "" {
		b, err := os.ReadFile(*source)
		if err != nil {
			fmt.Fprintln(stderr, "antislop:", err)
			return 2
		}
		srcText = string(b)
	}
	findings, lang := analyze(db, text, srcText, *source != "")
	hard, soft := 0, 0
	for _, f := range findings {
		if f.Hard {
			hard++
		} else {
			soft++
		}
	}
	if *asJSON {
		if findings == nil {
			findings = []Finding{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{"file": name, "source": *source, "source_lang": lang, "hard": hard, "soft": soft, "findings": findings})
	} else {
		for _, f := range findings {
			sev := "soft"
			if f.Hard {
				sev = "HARD"
			}
			loc := fmt.Sprintf("%d:%d", f.Line, f.Col)
			if f.Where == "source" {
				loc = "src " + loc
			}
			fmt.Fprintf(stdout, "%s %-11s %-9s «%s»  %s\n", sev, f.Rule, loc, oneLine(f.Match), f.Hint)
		}
		if lang != "" {
			fmt.Fprintf(stdout, "%s: %d hard, %d soft (source %s)\n", name, hard, soft, lang)
		} else {
			fmt.Fprintf(stdout, "%s: %d hard, %d soft\n", name, hard, soft)
		}
	}
	if hard > 0 {
		return 1
	}
	return 0
}

// analyze runs the text rules on text and, when hasSource, the cross-text checks against src.
// It returns the findings and the source language ("en", "ru", or "" without a source).
func analyze(db *data, text, src string, hasSource bool) ([]Finding, string) {
	out := newDoc(text)
	findings := check(db, out)
	if !hasSource {
		return findings, ""
	}
	sd := newDoc(src)
	if !isEnglish(sd) {
		return append(findings, fidelity(db, sd, out, false)...), "ru"
	}
	findings = mergeGlossary(findings, glossaryCheck(db, sd, out))
	return append(findings, fidelity(db, sd, out, true)...), "en"
}

func readInput(name string, stdin io.Reader) (string, error) {
	if name == "-" {
		b, err := io.ReadAll(stdin)
		return string(b), err
	}
	b, err := os.ReadFile(name)
	return string(b), err
}

func oneLine(s string) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) > 90 {
		return string(r[:87]) + "…"
	}
	return string(r)
}

// ---------------------------------------------------------------- data

// tableFile and dataFile mirror data/*.json.
type tableFile struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type dataFile struct {
	Files []tableFile `json:"files"`
}

// col returns a getter for the named column of t's rows.
func (t tableFile) col(name string) (func(row []string) string, error) {
	for i, c := range t.Columns {
		if c == name {
			return func(row []string) string { return row[i] }, nil
		}
	}
	return nil, fmt.Errorf("%s: no column %q", t.Name, name)
}

// wordPat matches one Russian word: an optional prefix, a stem and an ending from a set.
type wordPat struct {
	prefixes []string        // always holds ""
	stem     string          // normalized (lower case, ё → е)
	endings  map[string]bool // allowed endings; nil means the bare stem only
	number   bool            // "#": any number
}

// phrase is a sequence of word patterns that must match consecutive tokens.
type phrase []wordPat

// enWord matches one English word: a literal, a prefix ("stem*") or a number ("#").
type enWord struct {
	lit            string
	prefix, number bool
}

type enPhrase []enWord

type bannedRow struct {
	Term, Kind, UseInstead, Example string
	pats                            []phrase
}

type allowedRow struct {
	Term, Class string
	pats        []phrase
}

type domainRow struct {
	Term                  string
	pats                  []phrase
	domainCues, slangCues []string
	Note                  string
}

type glossRow struct {
	English, Preferred, AlsoOK, Never, Note string
	en                                      []enPhrase
	never                                   []phrase
}

type hedgeRow struct {
	English, Strength, Russian string
	en                         []enPhrase
	ru                         []phrase
}

type data struct {
	banned   []bannedRow
	allowed  []allowedRow
	domain   []domainRow
	glossary []glossRow
	hedges   []hedgeRow
}

var (
	validKinds     = map[string]bool{"slang": true, "cyrillized": true, "metaphor": true, "pipeline": true}
	validClasses   = map[string]bool{"latin": true, "loanword": true, "glued": true}
	strengthOrder  = []string{"possible", "probable", "unlikely", "usual", "often", "approx", "min", "max", "advice", "must", "certain"}
	enOnlyStrength = map[string]bool{"advice": true, "must": true}
	tickRe         = regexp.MustCompile("`([^`]+)`")
)

func loadData() (*data, error) {
	files := map[string]tableFile{}
	for _, name := range []string{"lexicon.json", "glossary.json", "hedges.json"} {
		b, err := dataFS.ReadFile("data/" + name)
		if err != nil {
			return nil, err
		}
		var df dataFile
		if err := json.Unmarshal(b, &df); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, t := range df.Files {
			files[t.Name] = t
		}
	}
	db := &data{}
	type spec struct {
		file string
		cols []string
		add  func(c []string) error
	}
	specs := []spec{
		{"ru-non-approved", []string{"term", "kind", "use instead", "example"}, func(c []string) error {
			if !validKinds[c[1]] {
				return fmt.Errorf("bad kind %q for %s", c[1], c[0])
			}
			pats, err := parseTerms(c[0])
			db.banned = append(db.banned, bannedRow{Term: c[0], Kind: c[1], UseInstead: c[2], Example: c[3], pats: pats})
			return err
		}},
		{"ru-allowed", []string{"term", "class"}, func(c []string) error {
			if !validClasses[c[1]] {
				return fmt.Errorf("bad class %q for %s", c[1], c[0])
			}
			pats, err := parseTerms(c[0])
			db.allowed = append(db.allowed, allowedRow{Term: c[0], Class: c[1], pats: pats})
			return err
		}},
		{"ru-domain", []string{"term", "domain cues", "slang cues", "note"}, func(c []string) error {
			pats, err := parseTerms(c[0])
			db.domain = append(db.domain, domainRow{Term: c[0], pats: pats, domainCues: splitCues(c[1]), slangCues: splitCues(c[2]), Note: c[3]})
			return err
		}},
		{"glossary-en-ru", []string{"English term", "Russian (preferred)", "also OK", "never", "note"}, func(c []string) error {
			r := glossRow{English: c[0], Preferred: c[1], AlsoOK: c[2], Never: c[3], Note: c[4]}
			var err error
			if r.en, err = parseEnglish(c[0]); err != nil {
				return err
			}
			if c[3] != "—" {
				if r.never, err = parseTerms(strings.Join(ticked(c[3]), ",")); err != nil {
					return err
				}
			}
			db.glossary = append(db.glossary, r)
			return nil
		}},
		{"hedges-en-ru", []string{"English hedge", "strength", "Russian renderings"}, func(c []string) error {
			if !contains(strengthOrder, c[1]) {
				return fmt.Errorf("bad strength %q", c[1])
			}
			r := hedgeRow{English: c[0], Strength: c[1], Russian: c[2]}
			var err error
			if r.en, err = parseEnglish(c[0]); err != nil {
				return err
			}
			r.ru, err = parseTerms(strings.Join(ticked(c[2]), ","))
			db.hedges = append(db.hedges, r)
			return err
		}},
	}
	for _, s := range specs {
		t, ok := files[s.file]
		if !ok || len(t.Rows) == 0 {
			return nil, fmt.Errorf("%s: missing or empty", s.file)
		}
		var getters []func([]string) string
		for _, c := range s.cols {
			g, err := t.col(c)
			if err != nil {
				return nil, err
			}
			getters = append(getters, g)
		}
		for _, row := range t.Rows {
			cells := make([]string, len(getters))
			for i, g := range getters {
				cells[i] = g(row)
			}
			if err := s.add(cells); err != nil {
				return nil, fmt.Errorf("%s: %w", s.file, err)
			}
		}
	}
	return db, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// ticked returns the contents of the backtick spans in a cell.
func ticked(cell string) []string {
	var out []string
	for _, m := range tickRe.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}

func parseEnglish(cell string) ([]enPhrase, error) {
	var out []enPhrase
	for _, p := range ticked(cell) {
		var ph enPhrase
		for _, w := range strings.Fields(strings.ToLower(p)) {
			switch {
			case w == "#":
				ph = append(ph, enWord{number: true})
			case strings.HasSuffix(w, "*"):
				ph = append(ph, enWord{lit: strings.TrimSuffix(w, "*"), prefix: true})
			default:
				ph = append(ph, enWord{lit: w})
			}
		}
		if len(ph) > 0 {
			out = append(out, ph)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no English pattern in %q", cell)
	}
	return out, nil
}

func (w enWord) match(s string) bool {
	switch {
	case w.number:
		return isNumber(s)
	case w.prefix:
		return strings.HasPrefix(s, w.lit)
	}
	return s == w.lit
}

// ---------------------------------------------------------------- Russian patterns

var (
	nounEnd = []string{"", "а", "я", "у", "ю", "ом", "ем", "е", "ы", "и", "ов", "ев", "ей", "ам", "ям", "ами", "ями",
		"ах", "ях", "ой", "ою", "ь", "ью", "ый", "ий", "ая", "яя", "ое", "ее", "ые", "ие", "ого", "его", "ому", "ему",
		"ым", "им", "ых", "их", "ую", "юю", "ыми", "ими"}
	adjEnd   = []string{"ий", "ый", "ая", "яя", "ое", "ее", "ие", "ые", "ого", "его", "ому", "ему", "ым", "им", "ых", "их", "ую", "юю", "ой", "ыми", "ими"}
	vThemes  = []string{"", "а", "я", "и", "е", "ну", "ова", "ирова", "иру", "у", "ыва", "ива"}
	vTails   = []string{"ть", "ться", "ю", "юсь", "ет", "ется", "ешь", "ем", "ете", "ют", "ются", "ут", "утся", "ит", "ится", "ят", "ятся", "л", "ла", "ло", "ли", "лся", "лась", "лось", "лись", "н", "на", "но", "ны", "т", "та", "то", "ты", "в", "вши", "я", "ясь"}
	partBase = []string{"ющ", "ущ", "ящ", "ащ", "вш", "нн", "т", "ем", "им", "н"}

	nounSet = toSet(nounEnd)
	verbSet = buildVerbSet()
)

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// buildVerbSet returns theme + tail combinations. No form is the bare stem, so «молч:v» never matches «молча».
func buildVerbSet() map[string]bool {
	tails := append([]string{}, vTails...)
	for _, b := range partBase {
		for _, a := range adjEnd {
			tails = append(tails, b+a)
		}
	}
	m := map[string]bool{}
	for _, th := range vThemes {
		for _, t := range tails {
			m[th+t] = true
		}
	}
	return m
}

// norm lower-cases a word and folds ё to е.
func norm(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}

func (p wordPat) match(w string) bool {
	if p.number {
		return isNumber(w)
	}
	for _, pre := range p.prefixes {
		s := pre + p.stem
		if !strings.HasPrefix(w, s) {
			continue
		}
		rest := w[len(s):]
		if p.endings == nil {
			if rest == "" {
				return true
			}
			continue
		}
		if p.endings[rest] {
			return true
		}
	}
	return false
}

func isNumber(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// parseWord parses "[за,про]стем:nv", "стем", or "#".
func parseWord(s string) (wordPat, error) {
	if s == "#" {
		return wordPat{number: true}, nil
	}
	p := wordPat{prefixes: []string{""}}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return p, fmt.Errorf("unclosed [ in %q", s)
		}
		for _, pre := range strings.Split(s[1:end], ",") {
			if pre = norm(strings.TrimSpace(pre)); pre != "" {
				p.prefixes = append(p.prefixes, pre)
			}
		}
		s = s[end+1:]
	}
	stem, class, hasClass := strings.Cut(s, ":")
	p.stem = norm(stem)
	if p.stem == "" {
		return p, fmt.Errorf("empty stem in %q", s)
	}
	if hasClass {
		p.endings = map[string]bool{}
		for _, c := range class {
			switch c {
			case 'n':
				for e := range nounSet {
					p.endings[e] = true
				}
			case 'v':
				for e := range verbSet {
					p.endings[e] = true
				}
			default:
				return p, fmt.Errorf("unknown class %q in %q (want n, v)", c, s)
			}
		}
	}
	return p, nil
}

// parseTerms parses a term cell: patterns separated by commas outside [...], each optionally in backticks.
func parseTerms(cell string) ([]phrase, error) {
	var parts []string
	depth, start := 0, 0
	for i, r := range cell {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, cell[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, cell[start:])
	var out []phrase
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), "`")
		if part == "" {
			continue
		}
		var ph phrase
		for _, w := range strings.Fields(part) {
			wp, err := parseWord(w)
			if err != nil {
				return nil, err
			}
			ph = append(ph, wp)
		}
		out = append(out, ph)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no pattern in %q", cell)
	}
	return out, nil
}

func splitCues(cell string) []string {
	var out []string
	for _, c := range strings.Split(cell, ",") {
		if c = norm(strings.TrimSpace(c)); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// matchesOne reports whether a single normalized word matches any one-word pattern.
func matchesOne(pats []phrase, w string) bool {
	for _, ph := range pats {
		if len(ph) == 1 && ph[0].match(w) {
			return true
		}
	}
	return false
}

// allowedWord reports whether a normalized word matches an allowed row of one of the given classes.
func (db *data) allowedWord(w string, classes ...string) bool {
	for _, row := range db.allowed {
		if contains(classes, row.Class) && matchesOne(row.pats, w) {
			return true
		}
	}
	return false
}

func (db *data) domainFor(w string) *domainRow {
	for i := range db.domain {
		if matchesOne(db.domain[i].pats, w) {
			return &db.domain[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------- text model

// doc is a markdown text with code marked. Positions are rune indexes.
type doc struct {
	r          []rune
	code       []bool     // rune is inside a code span, a fenced block or a URL
	spans      []codeSpan // inline code spans and fenced blocks, in order
	lineStarts []int
	tokens     []token
}

type codeSpan struct {
	start, end int // rune range including the backticks or fences
	content    string
	fenced     bool
}

type part struct {
	start, end int
	text       string
}

// token is a prose word: letters, digits and '_', joined by inner '-' or apostrophes.
type token struct {
	start, end int
	text       string
	parts      []part // split on '-' and apostrophes; one part when there is no joiner
}

var urlRe = regexp.MustCompile(`https?://[^\s)>\]]+`)

func newDoc(text string) *doc {
	d := &doc{r: []rune(text)}
	d.code = make([]bool, len(d.r))
	d.lineStarts = []int{0}
	for i, c := range d.r {
		if c == '\n' {
			d.lineStarts = append(d.lineStarts, i+1)
		}
	}
	d.maskCode()
	for _, m := range urlRe.FindAllStringIndex(text, -1) {
		a := len([]rune(text[:m[0]]))
		d.mark(a, a+len([]rune(text[m[0]:m[1]])))
	}
	d.tokenize()
	return d
}

func (d *doc) lineEnd(start int) int {
	for i := start; i < len(d.r); i++ {
		if d.r[i] == '\n' {
			return i
		}
	}
	return len(d.r)
}

func (d *doc) mark(a, b int) {
	for i := a; i < b; i++ {
		d.code[i] = true
	}
}

// maskCode marks fenced blocks (``` or ~~~ lines) and inline backtick spans. A span closes on the same line.
func (d *doc) maskCode() {
	var fence []rune
	fenceStart := 0
	var body []string
	for _, ls := range d.lineStarts {
		le := d.lineEnd(ls)
		line := string(d.r[ls:le])
		trim := strings.TrimSpace(line)
		isFence := strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~")
		switch {
		case fence == nil && isFence:
			fence = []rune(trim[:3])
			fenceStart = ls
			body = nil
			d.mark(ls, le)
		case fence != nil:
			d.mark(ls, le)
			if isFence && strings.HasPrefix(trim, string(fence)) {
				d.spans = append(d.spans, codeSpan{start: fenceStart, end: le, content: strings.Join(body, "\n"), fenced: true})
				fence = nil
				continue
			}
			body = append(body, strings.TrimRight(line, " \t\r"))
		default:
			d.maskInline(ls, le)
		}
	}
	if fence != nil { // unclosed fence: everything after it is code
		d.spans = append(d.spans, codeSpan{start: fenceStart, end: len(d.r), content: strings.Join(body, "\n"), fenced: true})
	}
}

func (d *doc) maskInline(ls, le int) {
	for i := ls; i < le; i++ {
		if d.r[i] != '`' {
			continue
		}
		n := 0
		for i+n < le && d.r[i+n] == '`' {
			n++
		}
		closeAt := -1
		for j := i + n; j < le; j++ {
			if d.r[j] != '`' {
				continue
			}
			m := 0
			for j+m < le && d.r[j+m] == '`' {
				m++
			}
			if m == n {
				closeAt = j
				break
			}
			j += m - 1
		}
		if closeAt < 0 {
			i += n - 1
			continue
		}
		content := string(d.r[i+n : closeAt])
		if len(content) >= 2 && strings.HasPrefix(content, " ") && strings.HasSuffix(content, " ") && strings.TrimSpace(content) != "" {
			content = content[1 : len(content)-1]
		}
		d.spans = append(d.spans, codeSpan{start: i, end: closeAt + n, content: content})
		d.mark(i, closeAt+n)
		i = closeAt + n - 1
	}
}

func isWordRune(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' }

func isJoiner(c rune) bool { return c == '-' || c == '\'' || c == '’' }

func (d *doc) prose(i int) bool { return i >= 0 && i < len(d.r) && !d.code[i] }

func (d *doc) tokenize() {
	i := 0
	for i < len(d.r) {
		if !d.prose(i) || !isWordRune(d.r[i]) {
			i++
			continue
		}
		start := i
		var parts []part
		ps := i
		for i < len(d.r) {
			if d.prose(i) && isWordRune(d.r[i]) {
				i++
				continue
			}
			if d.prose(i) && isJoiner(d.r[i]) && i > start && isWordRune(d.r[i-1]) && d.prose(i+1) && isWordRune(d.r[i+1]) {
				parts = append(parts, part{ps, i, string(d.r[ps:i])})
				i++
				ps = i
				continue
			}
			break
		}
		parts = append(parts, part{ps, i, string(d.r[ps:i])})
		d.tokens = append(d.tokens, token{start: start, end: i, text: string(d.r[start:i]), parts: parts})
	}
}

// pos returns the 1-based line and rune column of rune index i.
func (d *doc) pos(i int) (int, int) {
	lo, hi := 0, len(d.lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if d.lineStarts[mid] <= i {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1, i - d.lineStarts[lo] + 1
}

func hasLatin(s string) bool {
	for _, c := range s {
		if c < 128 && unicode.IsLetter(c) {
			return true
		}
	}
	return false
}

func hasCyr(s string) bool {
	for _, c := range s {
		if unicode.Is(unicode.Cyrillic, c) {
			return true
		}
	}
	return false
}

// isEnglish reports whether d's prose has more Latin letters than Cyrillic ones.
func isEnglish(d *doc) bool {
	lat, cyr := 0, 0
	for i, c := range d.r {
		switch {
		case !d.prose(i):
		case c < 128 && unicode.IsLetter(c):
			lat++
		case unicode.Is(unicode.Cyrillic, c):
			cyr++
		}
	}
	return lat > cyr
}

// sentence is a rune range of prose. Lines split sentences first, then . ! ? … followed by space and a capital.
type sentence struct{ start, end int }

func (d *doc) sentences() []sentence {
	var out []sentence
	add := func(a, b int) {
		for a < b && unicode.IsSpace(d.r[a]) {
			a++
		}
		for b > a && unicode.IsSpace(d.r[b-1]) {
			b--
		}
		for i := a; i < b; i++ { // keep only ranges that hold prose words
			if d.prose(i) && isWordRune(d.r[i]) {
				out = append(out, sentence{a, b})
				return
			}
		}
	}
	for _, ls := range d.lineStarts {
		le := d.lineEnd(ls)
		st := ls
		for i := ls; i < le; i++ {
			if !d.prose(i) || !strings.ContainsRune(".!?…", d.r[i]) {
				continue
			}
			j := i + 1
			for j < le && strings.ContainsRune(".!?…»\")*", d.r[j]) && d.prose(j) {
				j++
			}
			if j < le && !unicode.IsSpace(d.r[j]) {
				continue
			}
			k := j
			for k < le && unicode.IsSpace(d.r[k]) {
				k++
			}
			if k < le && unicode.IsLower(d.r[k]) {
				continue // «т. е.», «и т. д.», a lowercase continuation
			}
			add(st, j)
			st = j
			i = j - 1
		}
		add(st, le)
	}
	return out
}

// tokensIn returns the indexes of tokens inside [a, b).
func (d *doc) tokensIn(a, b int) []int {
	var out []int
	for i, t := range d.tokens {
		if t.start >= a && t.end <= b {
			out = append(out, i)
		}
	}
	return out
}

// inlineSpansIn counts inline (not fenced) code spans that start inside [a, b).
func (d *doc) inlineSpansIn(a, b int) int {
	n := 0
	for _, s := range d.spans {
		if !s.fenced && s.start >= a && s.start < b {
			n++
		}
	}
	return n
}

// sentenceOf returns the sentence that holds rune index i (or the whole doc).
func sentenceOf(ss []sentence, i int, total int) sentence {
	for _, s := range ss {
		if i >= s.start && i < s.end {
			return s
		}
	}
	return sentence{0, total}
}

// cueWords returns lower-case words of [a, b), code included, split on everything but letters, digits and '_'.
func (d *doc) cueWords(a, b int) []string {
	return strings.FieldsFunc(norm(string(d.r[a:b])), func(c rune) bool { return !isWordRune(c) })
}

func hasCue(words, cues []string) bool {
	for _, w := range words {
		for _, c := range cues {
			if strings.HasPrefix(w, c) {
				return true
			}
		}
	}
	return false
}

// proseText returns d's text with code runes blanked, so regexes see prose only.
func (d *doc) proseText() string {
	r := make([]rune, len(d.r))
	for i, c := range d.r {
		if d.code[i] && c != '\n' {
			c = ' '
		}
		r[i] = c
	}
	return string(r)
}

// ---------------------------------------------------------------- text rules

// Finding is one problem. Hard findings make the CLI exit 1.
type Finding struct {
	Rule  string `json:"rule"` // lexicon | glued | glossary | fidelity | domain | latin-prose | length | facts
	Kind  string `json:"kind,omitempty"`
	Term  string `json:"term,omitempty"` // lexicon or glossary row term cell
	Hard  bool   `json:"hard"`
	Where string `json:"where"` // "text", or "source" for a fidelity item missing from the text
	Line  int    `json:"line"`
	Col   int    `json:"col"`
	Match string `json:"match"`
	Hint  string `json:"hint"`
	start int
	end   int
}

const (
	maxWords  = 25
	maxBreaks = 2 // ≥3 clause breaks → "facts"
)

var labelRe = regexp.MustCompile(`^[A-Za-z]{1,2}\d+$`) // N4, P3, v1, S19

func (d *doc) finding(rule, kind string, hard bool, a, b int, hint string) Finding {
	line, col := d.pos(a)
	return Finding{Rule: rule, Kind: kind, Hard: hard, Where: "text", Line: line, Col: col,
		Match: string(d.r[a:b]), Hint: hint, start: a, end: b}
}

// check runs every text rule (all but fidelity and glossary) on d.
func check(db *data, d *doc) []Finding {
	ss := d.sentences()
	var out []Finding
	lexTok := map[int]bool{}

	out = append(out, checkLexicon(db, d, ss, lexTok)...)
	out = append(out, checkPhrases(db, d, lexTok)...)

	for ti, t := range d.tokens {
		lat, cyr := hasLatin(t.text), hasCyr(t.text)
		switch {
		case lat && cyr:
			if db.allowedWord(norm(t.text), "glued", "loanword") || negatedLatin(t) {
				continue
			}
			hint := "латиница + русское слово: имя в backticks отдельно от русского слова, или русское слово целиком"
			if strings.ContainsAny(t.text, "'’") {
				hint = "окончание через апостроф: `имя` в backticks без окончания + русское слово"
			} else if len(t.parts) == 1 {
				hint = "смесь латиницы и кириллицы внутри одного слова"
			}
			out = append(out, d.finding("glued", "", true, t.start, t.end, hint))
		case lat && !lexTok[ti]:
			if latinAllowed(db, t) {
				continue
			}
			out = append(out, d.finding("latin-prose", "", false, t.start, t.end,
				"латинское слово в тексте: имя кода → в backticks; обычное слово → по-русски"))
		}
	}

	for _, s := range ss {
		words := len(d.tokensIn(s.start, s.end)) + d.inlineSpansIn(s.start, s.end)
		if words > maxWords {
			out = append(out, d.finding("length", "", false, s.start, s.end,
				fmt.Sprintf("%d слов > %d: разбить, один факт на предложение", words, maxWords)))
		}
		if n := clauseBreaks(d, s); n > maxBreaks {
			out = append(out, d.finding("facts", "", false, s.start, s.end,
				fmt.Sprintf("%d разрывов (, ; — а но при этом): похоже на цепочку из 3+ фактов", n)))
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// domainSense decides a word at rune c. For a ru-domain term it returns the row and
// "slang-cue" (the sentence has a slang cue: hard), "domain" (the text has a domain cue: soft) or "no-cue" (hard).
func domainSense(db *data, d *doc, ss []sentence, w string, c int) (*domainRow, string) {
	dr := db.domainFor(w)
	if dr == nil {
		return nil, ""
	}
	s := sentenceOf(ss, c, len(d.r))
	switch {
	case hasCue(d.cueWords(s.start, s.end), dr.slangCues):
		return dr, "slang-cue"
	case hasCue(d.cueWords(0, len(d.r)), dr.domainCues):
		return dr, "domain"
	}
	return dr, "no-cue"
}

// candidates returns the token and, for a hyphenated token, each part.
func candidates(t token) []part {
	cands := []part{{t.start, t.end, t.text}}
	if len(t.parts) > 1 {
		cands = append(cands, t.parts...)
	}
	return cands
}

// checkLexicon matches one-word rows against each token and each hyphen part.
func checkLexicon(db *data, d *doc, ss []sentence, lexTok map[int]bool) []Finding {
	var out []Finding
	for ti, t := range d.tokens {
		if db.allowedWord(norm(t.text), "loanword", "glued", "latin") {
			continue
		}
		seen := map[int]bool{}
		for ci, c := range candidates(t) {
			w := norm(c.text)
			if ci > 0 && db.allowedWord(w, "loanword", "latin") {
				continue
			}
			for ri, row := range db.banned {
				if seen[ri] || !matchesOne(row.pats, w) {
					continue
				}
				seen[ri] = true
				lexTok[ti] = true
				hint := row.Kind + ": → " + row.UseInstead
				dr, sense := domainSense(db, d, ss, w, c.start)
				if sense == "domain" {
					f := d.finding("domain", row.Kind, false, c.start, c.end, dr.Note)
					f.Term = row.Term
					out = append(out, f)
					continue
				}
				if sense == "slang-cue" {
					hint += " (в предложении есть признак сленга)"
				}
				f := d.finding("lexicon", row.Kind, true, c.start, c.end, hint)
				f.Term = row.Term
				out = append(out, f)
			}
		}
	}
	return out
}

// phraseAt reports whether phrase ph matches tokens i, i+1, … separated only by spaces.
func phraseAt(d *doc, i int, ph phrase) bool {
	if i+len(ph) > len(d.tokens) {
		return false
	}
	for k, wp := range ph {
		t := d.tokens[i+k]
		if !wp.match(norm(t.text)) || (k > 0 && !onlySpaces(d, d.tokens[i+k-1].end, t.start)) {
			return false
		}
	}
	return true
}

// enPhraseAt is phraseAt for an English pattern.
func enPhraseAt(d *doc, i int, ph enPhrase) bool {
	if i+len(ph) > len(d.tokens) {
		return false
	}
	for k, w := range ph {
		t := d.tokens[i+k]
		if !w.match(strings.ToLower(t.text)) || (k > 0 && !onlySpaces(d, d.tokens[i+k-1].end, t.start)) {
			return false
		}
	}
	return true
}

// checkPhrases matches multi-word rows against runs of tokens separated only by spaces.
func checkPhrases(db *data, d *doc, lexTok map[int]bool) []Finding {
	var out []Finding
	for _, row := range db.banned {
		for _, ph := range row.pats {
			if len(ph) < 2 {
				continue
			}
			for i := range d.tokens {
				if !phraseAt(d, i, ph) {
					continue
				}
				for k := range ph {
					lexTok[i+k] = true
				}
				f := d.finding("lexicon", row.Kind, true, d.tokens[i].start, d.tokens[i+len(ph)-1].end,
					row.Kind+": → "+row.UseInstead)
				f.Term = row.Term
				out = append(out, f)
			}
		}
	}
	return out
}

func onlySpaces(d *doc, a, b int) bool {
	if a >= b {
		return false
	}
	for i := a; i < b; i++ {
		if !d.prose(i) || (d.r[i] != ' ' && d.r[i] != '\t' && d.r[i] != '\u00a0') {
			return false
		}
	}
	return true
}

// negatedLatin: «не-ASCII», «не-IPv4» — the Russian prefix «не-» on a Latin name is standard spelling, not glue.
func negatedLatin(t token) bool {
	if len(t.parts) < 2 || norm(t.parts[0].text) != "не" {
		return false
	}
	for _, p := range t.parts[1:] {
		if hasCyr(p.text) {
			return false
		}
	}
	return true
}

// latinAllowed: the token or every part is an allowed Latin word, a number, one letter or a short label.
func latinAllowed(db *data, t token) bool {
	ok := func(s string) bool {
		return len([]rune(s)) == 1 || isNumber(s) || labelRe.MatchString(s) || db.allowedWord(norm(s), "latin", "glued", "loanword")
	}
	if ok(t.text) {
		return true
	}
	if len(t.parts) == 1 {
		return false
	}
	for _, p := range t.parts {
		if !ok(p.text) {
			return false
		}
	}
	return true
}

// clauseBreaks counts , ; and a spaced dash in prose, plus «а», «но», «при этом» not right after a comma.
func clauseBreaks(d *doc, s sentence) int {
	n := 0
	for i := s.start; i < s.end; i++ {
		if !d.prose(i) {
			continue
		}
		switch d.r[i] {
		case ',', ';':
			n++
		case '—', '–':
			if i > 0 && i+1 < len(d.r) && unicode.IsSpace(d.r[i-1]) && unicode.IsSpace(d.r[i+1]) {
				n++
			}
		}
	}
	idx := d.tokensIn(s.start, s.end)
	for k, ti := range idx {
		t := d.tokens[ti]
		if !unicode.IsLower([]rune(t.text)[0]) {
			continue
		}
		conj := t.text == "а" || t.text == "но" ||
			(t.text == "при" && k+1 < len(idx) && d.tokens[idx[k+1]].text == "этом")
		if conj && !afterComma(d, t.start) {
			n++
		}
	}
	return n
}

func afterComma(d *doc, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if unicode.IsSpace(d.r[j]) {
			continue
		}
		return d.r[j] == ',' || d.r[j] == ';' || d.r[j] == '—' || d.r[j] == '–'
	}
	return false
}

// ---------------------------------------------------------------- cross-language checks

// enHas reports whether d's prose holds any of the English patterns.
func enHas(d *doc, pats []enPhrase) bool {
	for i := range d.tokens {
		for _, ph := range pats {
			if enPhraseAt(d, i, ph) {
				return true
			}
		}
	}
	return false
}

// glossaryCheck: for each glossary row whose English term is in src, every "never" variant in out is a hard finding.
func glossaryCheck(db *data, src, out *doc) []Finding {
	ss := out.sentences()
	var fs []Finding
	for _, row := range db.glossary {
		if len(row.never) == 0 || !enHas(src, row.en) {
			continue
		}
		hint := "→ " + row.Preferred
		if row.AlsoOK != "—" {
			hint += " (также: " + row.AlsoOK + ")"
		}
		hint = "glossary «" + strings.Join(ticked(row.English)[:1], "") + "» " + hint
		for _, t := range out.tokens {
			if db.allowedWord(norm(t.text), "loanword", "glued", "latin") {
				continue // «тест-кейс» is a listed loanword
			}
			for _, c := range candidates(t) {
				w := norm(c.text)
				if !matchesOne(row.never, w) {
					continue
				}
				if _, sense := domainSense(db, out, ss, w, c.start); sense == "domain" {
					continue // the lexicon reports the domain sense as a soft note
				}
				f := out.finding("glossary", "", true, c.start, c.end, hint)
				f.Term = row.English
				fs = append(fs, f)
				break
			}
		}
		for _, ph := range row.never {
			if len(ph) < 2 {
				continue
			}
			for i := range out.tokens {
				if phraseAt(out, i, ph) {
					f := out.finding("glossary", "", true, out.tokens[i].start, out.tokens[i+len(ph)-1].end, hint)
					f.Term = row.English
					fs = append(fs, f)
				}
			}
		}
	}
	return fs
}

// mergeGlossary adds glossary findings and drops lexicon findings on the same text: the glossary hint names the English term.
func mergeGlossary(text, gloss []Finding) []Finding {
	if len(gloss) == 0 {
		return text
	}
	var out []Finding
	for _, f := range text {
		dup := false
		for _, g := range gloss {
			if f.Rule == "lexicon" && f.start < g.end && g.start < f.end {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	seen := map[[2]int]bool{}
	for _, g := range gloss {
		if k := [2]int{g.start, g.end}; !seen[k] {
			seen[k] = true
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

var (
	fileLineRe = regexp.MustCompile(`[A-Za-z0-9_./-]*[A-Za-z0-9_-]\.[A-Za-z0-9]+:\d+(?:[-–]\d+)?`)
	pathRe     = regexp.MustCompile(`/?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+`)
	numberRe   = regexp.MustCompile(`\d+(?:[.,]\d+)?`)
	extRe      = regexp.MustCompile(`[A-Za-z0-9_-]\.[A-Za-z][A-Za-z0-9]{0,5}$`)
)

func runeIndex(s string, byteOff int) int { return len([]rune(s[:byteOff])) }

// hasNumber reports whether text holds n as a whole number (no digit right before or after it).
func hasNumber(text, n string) bool {
	re := regexp.MustCompile(`(^|[^0-9])` + regexp.QuoteMeta(n) + `([^0-9]|$)`)
	return re.MatchString(text)
}

// numberForms returns the spellings of n accepted in the text. With an English source, «2.5» may be «2,5»
// and «1,000» may be «1000» or «1 000».
func numberForms(n string, en bool) []string {
	forms := []string{n}
	if !en || !strings.ContainsAny(n, ".,") {
		return forms
	}
	sep := strings.IndexAny(n, ".,")
	head, tail := n[:sep], n[sep+1:]
	forms = append(forms, head+","+tail, head+"."+tail)
	if n[sep] == ',' && len(tail) == 3 {
		forms = append(forms, head+tail, head+" "+tail, head+"\u00a0"+tail)
	}
	return forms
}

// hit is a rune range of a hedge match.
type hit struct{ a, b int }

// hedgeHits returns the hedge matches in d for every row of a strength (at most one per start token).
func hedgeHits(db *data, d *doc, strength string, english bool) []hit {
	var hits []hit
	for i := range d.tokens {
		if english && strings.ToLower(d.tokens[i].text) == "could" && i+1 < len(d.tokens) && strings.ToLower(d.tokens[i+1].text) == "not" {
			continue // «could not connect» is a past fact
		}
		n := 0
		for _, row := range db.hedges {
			if row.Strength != strength {
				continue
			}
			if english {
				for _, ph := range row.en {
					if enPhraseAt(d, i, ph) && len(ph) > n {
						n = len(ph)
					}
				}
				continue
			}
			for _, ph := range row.ru {
				if phraseAt(d, i, ph) && len(ph) > n {
					n = len(ph)
				}
			}
		}
		if n > 0 {
			hits = append(hits, hit{d.tokens[i].start, d.tokens[i+n-1].end})
		}
	}
	return hits
}

func russianFamily(db *data, strength string) string {
	var out []string
	for _, row := range db.hedges {
		if row.Strength != strength {
			continue
		}
		for _, p := range ticked(row.Russian) {
			if strings.Contains(p, ":") { // «возможн:n» is a stem, not a word to show
				continue
			}
			out = append(out, strings.ReplaceAll(p, "#", "N"))
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return strings.Join(out, " / ")
}

// fidelity lists items of src missing from out (code spans, file:line, paths, numbers, hedges) and certainty that out adds.
func fidelity(db *data, src, out *doc, en bool) []Finding {
	var fs []Finding
	add := func(a, b int, kind, hint string) {
		f := src.finding("fidelity", kind, true, a, b, hint)
		f.Where = "source"
		fs = append(fs, f)
	}
	outText := string(out.r)

	outSpans := map[string]bool{}
	for _, s := range out.spans {
		outSpans[strings.TrimSpace(s.content)] = true
	}
	seen := map[string]bool{}
	for _, s := range src.spans {
		c := strings.TrimSpace(s.content)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if !outSpans[c] {
			add(s.start, s.end, "code", "код из исходника пропал или изменился (нужен byte-identical в backticks)")
		}
	}

	srcText := string(src.r)
	covered := map[string]bool{}
	for _, m := range fileLineRe.FindAllStringIndex(srcText, -1) {
		v := srcText[m[0]:m[1]]
		covered[v] = true
		if !seen["fl:"+v] && !strings.Contains(outText, v) {
			add(runeIndex(srcText, m[0]), runeIndex(srcText, m[1]), "file:line", "файл:строка из исходника пропал")
		}
		seen["fl:"+v] = true
	}

	prose := src.proseText()
	for _, m := range pathRe.FindAllStringIndex(prose, -1) {
		v := strings.TrimRight(prose[m[0]:m[1]], ".")
		last := v[strings.LastIndex(v, "/")+1:]
		// A path starts with "/" or "./", or ends in a file name with an extension. «retry/fallback/detectReader» is not one.
		isPath := strings.HasPrefix(v, "/") || strings.HasPrefix(v, "./") || extRe.MatchString(last)
		if !isPath || !hasLatin(v) || seen["p:"+v] || coveredBy(covered, v) {
			continue
		}
		seen["p:"+v] = true
		if !strings.Contains(outText, v) {
			add(runeIndex(prose, m[0]), runeIndex(prose, m[0])+len([]rune(v)), "path", "путь из исходника пропал")
		}
	}

	for _, m := range numberRe.FindAllStringIndex(prose, -1) {
		v := prose[m[0]:m[1]]
		if seen["n:"+v] {
			continue
		}
		seen["n:"+v] = true
		found := false
		for _, form := range numberForms(v, en) {
			found = found || hasNumber(outText, form)
		}
		if !found {
			add(runeIndex(prose, m[0]), runeIndex(prose, m[1]), "number", "число из исходника пропало")
		}
	}

	for _, s := range strengthOrder {
		if !en && enOnlyStrength[s] {
			continue
		}
		sh, oh := hedgeHits(db, src, s, en), hedgeHits(db, out, s, false)
		if s == "certain" {
			if len(oh) > len(sh) {
				h := oh[len(sh)]
				fs = append(fs, out.finding("fidelity", "certainty", true, h.a, h.b,
					fmt.Sprintf("уверенность «%s»: в исходнике %d, в тексте %d — не добавлять уверенности, которой нет в исходнике", s, len(sh), len(oh))))
			}
			continue
		}
		if len(oh) < len(sh) {
			h := sh[len(oh)]
			add(h.a, h.b, "hedge", fmt.Sprintf("оговорка силы «%s»: в исходнике %d, в тексте %d → %s", s, len(sh), len(oh), russianFamily(db, s)))
		}
	}
	return fs
}

func coveredBy(fileLines map[string]bool, v string) bool {
	for fl := range fileLines {
		if strings.Contains(fl, v) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- lookup

type entry struct {
	Table string            `json:"table"`
	Cells map[string]string `json:"cells"`
	order []string
}

// termMatches reports whether the term's words match a Russian phrase or an English phrase.
func termMatches(words []string, ru []phrase, en []enPhrase) bool {
	for _, ph := range ru {
		if len(ph) != len(words) {
			continue
		}
		ok := true
		for i, wp := range ph {
			ok = ok && wp.match(norm(words[i]))
		}
		if ok {
			return true
		}
	}
	for _, ph := range en {
		if len(ph) != len(words) {
			continue
		}
		ok := true
		for i, w := range ph {
			ok = ok && w.match(strings.ToLower(words[i]))
		}
		if ok {
			return true
		}
	}
	return false
}

func lookupEntries(db *data, term string) []entry {
	words := strings.Fields(term)
	lower := norm(term)
	in := func(s string) bool { return len([]rune(lower)) >= 3 && strings.Contains(norm(s), lower) }
	var out []entry
	mk := func(table string, kv ...string) entry {
		e := entry{Table: table, Cells: map[string]string{}}
		for i := 0; i+1 < len(kv); i += 2 {
			e.Cells[kv[i]] = kv[i+1]
			e.order = append(e.order, kv[i])
		}
		return e
	}
	for _, r := range db.glossary {
		if termMatches(words, r.never, r.en) || in(r.Preferred) || in(r.AlsoOK) {
			out = append(out, mk("glossary-en-ru", "English term", r.English, "Russian (preferred)", r.Preferred, "also OK", r.AlsoOK, "never", r.Never, "note", r.Note))
		}
	}
	for _, r := range db.hedges {
		if termMatches(words, r.ru, r.en) {
			out = append(out, mk("hedges-en-ru", "English hedge", r.English, "strength", r.Strength, "Russian renderings", r.Russian))
		}
	}
	for _, r := range db.banned {
		if termMatches(words, r.pats, nil) {
			out = append(out, mk("ru-non-approved", "term", r.Term, "kind", r.Kind, "use instead", r.UseInstead, "example", r.Example))
		}
	}
	for _, r := range db.allowed {
		if termMatches(words, r.pats, nil) {
			out = append(out, mk("ru-allowed", "term", r.Term, "class", r.Class))
		}
	}
	for _, r := range db.domain {
		if termMatches(words, r.pats, nil) {
			out = append(out, mk("ru-domain", "term", r.Term, "note", r.Note))
		}
	}
	return out
}

func runLookup(db *data, terms []string, asJSON bool, stdout io.Writer) int {
	found := 0
	all := map[string][]entry{}
	for _, t := range terms {
		es := lookupEntries(db, t)
		found += len(es)
		all[t] = es
		if asJSON {
			continue
		}
		if len(es) == 0 {
			fmt.Fprintf(stdout, "%s: no entry\n", t)
			continue
		}
		for _, e := range es {
			var parts []string
			for _, k := range e.order {
				parts = append(parts, k+": "+e.Cells[k])
			}
			fmt.Fprintf(stdout, "%s → [%s] %s\n", t, e.Table, strings.Join(parts, " | "))
		}
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(all)
	}
	if found == 0 {
		return 1
	}
	return 0
}
