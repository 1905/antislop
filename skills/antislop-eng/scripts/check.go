// check is a mechanical checker for controlled technical English.
//
//	go run scripts/check.go FILE [--mode procedural|descriptive|auto] [--glossary TERMS.txt] [--json]
//	go run scripts/check.go --lookup WORD [WORD ...]
//
// Standard library only. The dictionary is embedded from dictionary.json.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed dictionary.json
var dictJSON []byte

// defaultGlossary is loaded on every check unless --no-default-glossary is given.
//
//go:embed glossary/software.txt
var defaultGlossary string

const usageText = `check — mechanical checker for controlled technical English.

Usage:
  go run scripts/check.go FILE [--mode procedural|descriptive|auto] [--glossary TERMS.txt] [--no-default-glossary] [--json]
  go run scripts/check.go --lookup WORD [WORD ...]
  cat text.md | go run scripts/check.go -

The built-in software glossary (scripts/glossary/software.txt) is always loaded; --glossary adds
your own terms on top; --no-default-glossary turns the built-in list off.

Flags can come before or after FILE.
  Words      non-approved words with their alternatives (1.1–1.3, 9.1); words not in the
             dictionary (technical nouns/verbs, 1.5/1.12, or replace them)
  Verbs      -ing forms (3.5), have/has/had + participle and been/being (3.4), passive (3.6)
  Sentences  more than 20 words procedural / 25 descriptive, counted per 8.4–8.7; semicolons (8.1);
             contractions (4.2); e.g./i.e./etc. (GR-6); noun clusters of 4+ words (2.1); he/she (GR-7)
  Paragraphs more than 6 sentences (6.6)
Code blocks and inline ` + "`code`" + ` spans are not checked; a span counts as one word (8.6).
Code-like tokens (paths, x.go:12, snake_case, camelCase, dotted.names, --flags, URLs) and
ALLCAPS acronyms are technical nouns; the report lists them ("auto_glossary" in --json).
Hedge words (may, might, probably, should, ...) get an approved rewrite of the same strength.

It is a heuristic, not a parser: expect some false positives. Give your technical nouns and
verbs in --glossary (one per line, multi-word allowed). The final judgement is the writer's.
`

// ---------------------------------------------------------------- text helpers

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// isUpperWord reports a word with capitals and no lower-case letters ("ABC", "A-B").
func isUpperWord(s string) bool {
	cased := false
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsTitle(r) {
			return false
		}
		cased = cased || unicode.IsUpper(r)
	}
	return cased
}

// isTitleCase reports text where each word starts with a capital and continues in lower case.
func isTitleCase(s string) bool {
	prevCased := false
	for _, r := range s {
		cased := unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
		if cased && ((prevCased && unicode.ToLower(r) != r) || (!prevCased && unicode.ToTitle(r) != r)) {
			return false
		}
		prevCased = cased
	}
	return true
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func hasAnySuffix(s string, sufs ...string) bool {
	return slices.ContainsFunc(sufs, func(x string) bool { return strings.HasSuffix(s, x) })
}

// boundary is \b with Unicode word characters.
func boundary(rs []rune, i int) bool {
	return (i > 0 && isWordRune(rs[i-1])) != (i < len(rs) && isWordRune(rs[i]))
}

// matchAt matches lit (case-insensitive) at rs[i:] after a word boundary, and before one if tail.
// It returns the end of the match, or -1.
func matchAt(rs []rune, i int, lit string, tail bool) int {
	l := []rune(lit)
	if !boundary(rs, i) || i+len(l) > len(rs) {
		return -1
	}
	for k, r := range l {
		if rs[i+k] != r && !strings.EqualFold(string(rs[i+k]), string(r)) {
			return -1
		}
	}
	if e := i + len(l); !tail || boundary(rs, e) {
		return e
	}
	return -1
}

// findBounded returns the first match of one of alts, trying them in order at each position.
func findBounded(s string, alts []string, tail bool) string {
	rs := []rune(s)
	for i := 0; i <= len(rs); i++ {
		for _, a := range alts {
			if e := matchAt(rs, i, a, tail); e >= 0 {
				return string(rs[i:e])
			}
		}
	}
	return ""
}

// replaceBounded replaces lit, as a whole word or phrase in any case, with repl.
func replaceBounded(s, lit, repl string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if e := matchAt(rs, i, lit, true); e > i {
			b.WriteString(repl)
			i = e
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// ---------------------------------------------------------------- regular expressions

const ws = `[\s\v\x{85}\p{Z}]`

var (
	// a list marker: 1. 1) 10a. (a) a. iv. - • * –; never a short word such as "it." in wrapped prose
	listMarkerRe  = regexp.MustCompile(`^` + ws + `*(\(?(\d{1,3}[a-z]?|[A-Za-z]|[ivxIVX]{2,4})[.)]|[-•*–]|\p{Nd}+\.)` + ws + `+`)
	labelRe       = regexp.MustCompile(`(?i)^(WARNING|CAUTION|NOTE|DANGER|NOTICE)` + ws + `*:` + ws + `*`)
	safetyLabelRe = regexp.MustCompile(`(?i)^(WARNING|CAUTION|DANGER)` + ws + `*:` + ws + `*`)
	noteRe        = regexp.MustCompile(`(?i)^NOTE` + ws + `*:`)
	headingHashRe = regexp.MustCompile(`^` + ws + `*#{1,6}` + ws)
	parenRe       = regexp.MustCompile(`\([^()]*\)`)
	quoteRe       = regexp.MustCompile(`"[^"]+"|“[^”]+”`)
	digitDotRe    = regexp.MustCompile(`(\p{Nd})\.(\p{Nd})`)
	abbrRe        = regexp.MustCompile(`(^|[^\pL\pN_])(e\.g\.|i\.e\.|etc\.|No\.|Fig\.|fig\.|a\.m\.|p\.m\.|vs\.|approx\.|max\.|min\.)`)
	formTokRe     = regexp.MustCompile(`[a-z][a-z'\-]*`)
	wordParenRe   = regexp.MustCompile(ws + `*\(.*?\)` + ws + `*`)
	rawTokRe      = regexp.MustCompile(`[A-Za-z][A-Za-z'\-_]*`)
	headWordsRe   = regexp.MustCompile(`[A-Za-z']+`)
	commaWordRe   = regexp.MustCompile(`,` + ws + `*([A-Za-z']+)`)
	chunkRe       = regexp.MustCompile(`[^\s\v\x{85}\p{Z}]+`)
)

// ---------------------------------------------------------------- dictionary

// apEntry is an approved word. Unknown fields (*_raw, *_table) are ignored; examples can be empty.
type apEntry struct {
	Word     string   `json:"word"`
	Pos      string   `json:"pos"`
	Meaning  []string `json:"meaning"`
	Examples []string `json:"examples"`
	ForOther string   `json:"for_other_meanings_use"`
	Forms    string   `json:"forms"`
	Note     string   `json:"note"`
}

type naEntry struct {
	Word         string   `json:"word"`
	Pos          string   `json:"pos"`
	UseInstead   []string `json:"use_instead"`
	Example      []string `json:"example"`
	WrongExample []string `json:"incorrect_example"`
	Note         string   `json:"note"`
}

// Dict holds the approved and non-approved word lists and the surface-form index.
type Dict struct {
	approved    map[string][]*apEntry
	nonApproved map[string][]*naEntry
	forms       map[string]string // every permitted surface form -> its approved base word
}

var loadDict = sync.OnceValues(func() (*Dict, error) { return parseDict(dictJSON) })

func parseDict(data []byte) (*Dict, error) {
	var raw struct {
		Approved    []apEntry `json:"approved"`
		NonApproved []naEntry `json:"non_approved"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	d := &Dict{approved: map[string][]*apEntry{}, nonApproved: map[string][]*naEntry{}, forms: map[string]string{}}
	var order []string
	for i := range raw.Approved {
		e := &raw.Approved[i]
		k := strings.ToLower(e.Word)
		if d.approved[k] == nil {
			order = append(order, k)
		}
		d.approved[k] = append(d.approved[k], e)
	}
	for i := range raw.NonApproved {
		e := &raw.NonApproved[i]
		full := strings.ToLower(e.Word)
		w := strings.TrimSpace(wordParenRe.ReplaceAllString(full, " ")) // "few (a few)" -> "few"
		d.nonApproved[w] = append(d.nonApproved[w], e)
		if w != full {
			d.nonApproved[full] = append(d.nonApproved[full], e)
		}
	}
	setdef := func(k, v string) {
		if _, ok := d.forms[k]; !ok {
			d.forms[k] = v
		}
	}
	for _, base := range order {
		for _, e := range d.approved[base] {
			setdef(base, base)
			for _, tok := range formTokRe.FindAllString(strings.ToLower(e.Forms+" "+e.Note), -1) {
				if !formStop[tok] && runeLen(tok) > 1 {
					setdef(tok, base)
				}
			}
			if e.Pos == "n" {
				for _, p := range pluralForms(base) {
					setdef(p, base)
				}
			}
			if e.Pos == "v" && e.Forms == "" {
				setdef(base+"s", base)
				setdef(base+"ed", base)
			}
		}
	}
	for _, f := range []string{"is", "was", "are", "were", "be"} {
		d.forms[f] = "be"
	}
	setdef("makes sure", "make sure")
	setdef("made sure", "make sure")
	return d, nil
}

func pluralForms(w string) []string {
	out := []string{w + "s"}
	if hasAnySuffix(w, "s", "x", "z", "ch", "sh") {
		out = append(out, w+"es")
	}
	if rs := []rune(w); len(rs) >= 2 && rs[len(rs)-1] == 'y' && !strings.ContainsRune("aeiou", rs[len(rs)-2]) {
		out = append(out, string(rs[:len(rs)-1])+"ies")
	}
	if strings.HasSuffix(w, "f") {
		out = append(out, w[:len(w)-1]+"ves")
	}
	if strings.HasSuffix(w, "fe") {
		out = append(out, w[:len(w)-2]+"ves")
	}
	return out
}

// ---------------------------------------------------------------- word lists

var formStop = words(`no other verb forms also for example use this word only as an adjective or the a in of not to do with
	that is it you and when but form same plural noun singular adverb these words before after used meaning meanings sense context refer`)

// approvedIng lists the only approved -ing forms (Rule 3.5).
var approvedIng = words("lighting opening routing servicing mating missing remaining something during")

var irregularParticiples = words(`been done given gone made put set shown seen held kept known taken written broken cut let read sent
	spent left found got worn torn bent built lost run won begun become come drawn driven fallen flown frozen hidden hit led lit met paid
	said sold spoken stuck thrown understood hung shut spread split`)

var beForms = words("is are was were be been being am")

var haveForms = words("have has had")

var numberWords = words(`zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen
	seventeen eighteen nineteen twenty thirty forty fifty sixty seventy eighty ninety hundred thousand million half quarter third fourth fifth`)

var determiners = words(`the a an this these its their your our each all some many no of front rear top bottom left right new old
	primary applicable correct two three four five six`)

// alwaysVerb holds modals and common verbs with no noun sense.
var alwaysVerb = words(`may might should would could shall ought ensure take perform provide obtain require allow enable indicate
	occur avoid achieve verify confirm assure determine establish maintain prevent proceed remain become seem appear consist contain
	possess acquire utilize utilise employ conduct carry commence cease terminate initiate activate deactivate locate relocate
	accomplish facilitate eliminate exceed persist arise happen depend rely refer reinstall retighten`)

var verbSlot = words("to you we they it can will must not do does did and then or always never carefully slowly immediately also only")

var functionWords = words(`a an the and or of to in on at for with by from as is are was were be not no if when that this these it
	you we they than then thus but also only each all its their`)

var conditionHeads = words("if when before after while until unless")

var imperativeHeads = words(`do make put set remove install connect disconnect turn press push pull open close start stop use apply
	examine measure tighten loosen attach hold keep wait read refer record replace discard clean fill drain lift lower move operate
	energize de-energize obey get go let cut lubricate tag torque adjust align release engage disengage select touch enter type click
	tap swipe scroll save download upload reboot restart update insert mount unscrew screw supply send give show write add count
	identify compare continue repeat always never only carefully slowly immediately then first also`)

var contractionSet = words("it's let's that's there's what's here's who's")

var multiWordPhrases = []string{"make sure", "makes sure", "made sure", "away from", "out of", "in front of", "because of",
	"adjacent to", "each other", "in progress", "put on", "come on", "go off", "aft of", "forward of", "inboard of", "outboard of",
	"upstream of", "downstream of", "for example", "as a result", "at the same time"}

var latinAlts = []string{"e.g.", "i.e.", "etc.", "etc", "et al.", "et al", "vs.", "viz."}
var genderAlts = []string{"he", "she", "him", "her", "his", "hers", "himself", "herself"}

type suffixRule struct{ suf, repl string }

var deinflect = []suffixRule{{"ies", "y"}, {"es", ""}, {"s", ""}, {"ed", ""}, {"ed", "e"}, {"ing", ""}, {"ing", "e"}, {"er", ""}, {"est", ""}, {"ly", ""}}

// hedgeHints keeps the strength of a hedged claim. Every capitalized word is approved (a test checks it).
var hedgeHints = map[string]string{
	"may":      `POSSIBLY, or "IT IS POSSIBLE THAT ..." (CAN only for ability or permission). Keep it a possibility.`,
	"might":    `POSSIBLY, or "IT IS POSSIBLE THAT ...". Keep it a possibility.`,
	"perhaps":  `POSSIBLY, or "IT IS POSSIBLE THAT ...". Keep it a possibility.`,
	"likely":   `"IT IS VERY POSSIBLE THAT ...". Keep it a possibility, not a fact.`,
	"probably": `"IT IS VERY POSSIBLE THAT ...". Keep it a possibility, not a fact.`,
	"probable": `"IT IS VERY POSSIBLE THAT ...", or "A VERY POSSIBLE RISK" for a danger. Keep it a possibility, not a fact.`,
	"unlikely": `JUDGE: "IT IS NOT VERY POSSIBLE THAT ...", or "THE RISK IS SMALL" for a danger. Do not write that it cannot occur.`,
	"should":   `JUDGE: advice -> "WE RECOMMEND THAT ..."; obligation -> MUST. Do not change advice into an order by accident.`,
}

// ---------------------------------------------------------------- word count (rules 8.4–8.7)

// oneWordRe finds the units that count as one word: parenthesis (8.5), quoted text, number with
// unit, identifier, abbreviation (8.6), hyphenated word (8.7), word. It runs on asciiWords(s),
// because \b and \w in Go are ASCII only.
var oneWordRe = func() *regexp.Regexp {
	units := strings.Fields(`°C °F % mm cm m km in ft kg g lb lbs oz psi kpa mpa bar nm n v kv mv a ma ka w kw mw hz khz mhz ghz
		ohm ohms Ω s ms min h hr hrs l ml gal rpm db c f k`)
	for i, u := range units {
		units[i] = regexp.QuoteMeta(u)
	}
	num := `[+\-−]?\d[\d,.:/]*`
	return regexp.MustCompile(`\([^()]*\)|"[^"]+"|“[^”]+”|` + num + ws + `?(?:` + strings.Join(units, "|") + `)\b|` + num +
		`|\b[A-Z0-9][A-Z0-9\-/.]*\d[A-Z0-9\-/.]*\b|\b[A-Z]{2,}(?:[-/][A-Z0-9]+)*\b|\b\w+(?:-\w+)+\b|\b[\w']+\b`)
}()

// asciiWords changes each non-ASCII letter or digit to "a", so that Go's ASCII \w sees a word character.
func asciiWords(s string) string {
	return strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsNumber(r)) {
			return 'a'
		}
		return r
	}, s)
}

// countWords counts the words of a sentence. List markers and safety labels do not count (8.4).
func countWords(sentence string) int {
	s := labelRe.ReplaceAllString(listMarkerRe.ReplaceAllString(strings.TrimSpace(sentence), ""), "")
	n := 0
	for _, t := range oneWordRe.FindAllString(asciiWords(s), -1) {
		if strings.IndexFunc(t, isWordRune) >= 0 {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- segmentation

func splitParagraphs(text string) [][]string {
	var paras [][]string
	var cur []string
	for _, line := range append(strings.Split(text, "\n"), "") {
		if strings.TrimSpace(line) != "" {
			cur = append(cur, line)
		} else if len(cur) > 0 {
			paras = append(paras, cur)
			cur = nil
		}
	}
	return paras
}

func isHeading(line string) bool {
	st := strings.TrimSpace(line)
	return headingHashRe.MatchString(line) || (len(strings.Fields(line)) <= 8 && isTitleCase(st) && !strings.HasSuffix(st, "."))
}

// splitSentences splits a block after . ! ? or : when a capital, a digit, or a quote or
// parenthesis before one comes next. A colon that ends a list intro ends a sentence (8.4).
func splitSentences(block string) []string {
	t := abbrRe.ReplaceAllStringFunc(block, func(m string) string {
		_, pre := utf8.DecodeRuneInString(m)
		if abbrRe.FindStringSubmatch(m)[1] == "" {
			pre = 0
		}
		return m[:pre] + strings.ReplaceAll(m[pre:], ".", "<DOT>")
	})
	rs := []rune(digitDotRe.ReplaceAllString(t, "${1}<DOT>${2}"))
	isAZ09 := func(k int) bool { return k < len(rs) && (rs[k] >= 'A' && rs[k] <= 'Z' || rs[k] >= '0' && rs[k] <= '9') }
	var out []string
	emit := func(p string) {
		if p = strings.TrimSpace(strings.ReplaceAll(p, "<DOT>", ".")); p != "" {
			out = append(out, p)
		}
	}
	last := 0
	for p := 1; p < len(rs); p++ {
		if !strings.ContainsRune(".!?:", rs[p-1]) || !unicode.IsSpace(rs[p]) {
			continue
		}
		q := p
		for q < len(rs) && unicode.IsSpace(rs[q]) {
			q++
		}
		if isAZ09(q) || (q < len(rs) && strings.ContainsRune(`"“(`, rs[q]) && isAZ09(q+1)) {
			emit(string(rs[last:p]))
			last, p = q, q
		}
	}
	emit(string(rs[last:]))
	return out
}

// ---------------------------------------------------------------- code skip

// placeholder returns an all-caps word for the text it stands for, and records the text in m.
// The word count makes it one word (8.6), and the word checks skip it.
func placeholder(prefix string, m map[string]string, text string) string {
	k := len(m)
	p := prefix + string([]byte{byte('A' + k/676%26), byte('A' + k/26%26), byte('A' + k%26)})
	m[p] = text
	return p
}

var codePlaceholderRe = regexp.MustCompile(`CODESPAN[A-Z]{3}`)

// restoreCode puts the original `code` spans back, for the examples in the report.
func restoreCode(s string, spans map[string]string) string {
	return codePlaceholderRe.ReplaceAllStringFunc(s, func(p string) string {
		if span, ok := spans[p]; ok {
			return span
		}
		return p
	})
}

func runLen(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// stripInlineCode replaces each `code` span with one word. Unclosed backticks stay as they are.
func stripInlineCode(line string, spans map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n, end := runLen(line[i:], '`'), -1
		for k := i + n; k < len(line) && end < 0; {
			if m := runLen(line[k:], '`'); m == n {
				end = k
			} else {
				k += max(m, 1)
			}
		}
		if end < 0 {
			b.WriteString(line[i : i+n])
			i += n
			continue
		}
		b.WriteString(placeholder("CODESPAN", spans, line[i:end+n]))
		i = end + n
	}
	return b.String()
}

// stripCode blanks code blocks (so they also end the paragraph) and collapses inline spans.
// A fence can have any indent (a fence in a nested list). An indented block (4 spaces or a tab)
// is code after a blank line, but not in a list, where the indent continues a list item.
// It returns the new text and the original spans.
func stripCode(text string) (string, map[string]string) {
	spans := map[string]string{}
	lines := strings.Split(text, "\n")
	var fenceCh byte
	fenceLen := 0
	indented, inList, afterBlank := false, false, true
	for i, l := range lines {
		body := strings.TrimLeft(l, " \t")
		isIndented := strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "\t")
		switch {
		case fenceLen > 0:
			if n := runLen(body, fenceCh); n >= fenceLen && strings.TrimSpace(body[n:]) == "" {
				fenceLen = 0
			}
			lines[i] = ""
		case strings.TrimSpace(body) == "":
			afterBlank = true
			continue
		case isIndented && (indented || (afterBlank && !inList)):
			indented = true
			lines[i] = ""
		default:
			indented = false
			for _, ch := range []byte{'`', '~'} {
				if n := runLen(body, ch); n >= 3 && !(ch == '`' && strings.Contains(body[n:], "`")) {
					fenceCh, fenceLen = ch, n
					break
				}
			}
			if fenceLen > 0 {
				lines[i] = ""
				break
			}
			if listMarkerRe.MatchString(l) {
				inList = true
			} else if afterBlank && !isIndented {
				inList = false
			}
			lines[i] = stripInlineCode(l, spans)
		}
		afterBlank = false
	}
	return strings.Join(lines, "\n"), spans
}

// ---------------------------------------------------------------- auto-glossary

var (
	flagRe     = regexp.MustCompile(`^--?[A-Za-z][A-Za-z0-9_-]*(=\S*)?$`)
	urlRe      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://\S+$`)
	pathLineRe = regexp.MustCompile(`^[\w./~-]*\.[A-Za-z0-9]+(:\d+)+$`)
	pathRe     = regexp.MustCompile(`^[\w.~/@:+-]+$`)
	fileRe     = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.(go|mod|sum|py|ts|tsx|js|jsx|mjs|cjs|json|ya?ml|toml|ini|cfg|conf|env|md|txt|csv|sql|sh|bash|zsh|rs|java|kt|rb|php|c|h|cc|cpp|hpp|proto|html|css|xml|lock|log|tf)$`)
	dotfileRe  = regexp.MustCompile(`^\.[A-Za-z][\w.-]*$`)
	dottedRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+$`)
	snakeRe    = regexp.MustCompile(`^_*[A-Za-z0-9]+(_+[A-Za-z0-9]+)*_*$`)
	mixedRe    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	callRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	acronymRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,5}$`)
	labelWords = words("WARNING CAUTION NOTE DANGER NOTICE")
	// suffixes named in prose about grammar, not command-line flags
	suffixMentions = words("-ing -ed -er -est -ly -s -es -ies")
)

// shouted reports a sentence written in capitals (as the dictionary examples are). Its capitalized
// words are ordinary words, not acronyms.
func shouted(s string) bool {
	caps, n := 0, 0
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(w) >= 2 {
			n++
			if isUpperWord(w) {
				caps++
			}
		}
	}
	return n >= 3 && caps*2 > n
}

// splitPunct separates leading and trailing prose punctuation (and a possessive 's) from a chunk.
func splitPunct(ch string) (pre, core, suf string) {
	core = strings.TrimLeft(ch, "(\"'“‘<[{")
	pre = ch[:len(ch)-len(core)]
	end := len(core)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(core[:end])
		// keep the ")" of a call such as fmt.Println()
		if !strings.ContainsRune(".,;:!?)]}\"'>”’", r) || (r == ')' && strings.Count(core[:end], "(") >= strings.Count(core[:end], ")")) {
			break
		}
		end -= size
	}
	for _, p := range []string{"'s", "’s"} {
		if strings.HasSuffix(core[:end], p) && end > len(p) {
			end -= len(p)
			break
		}
	}
	return pre, core[:end], core[end:]
}

// classifyCode returns the auto-glossary kind of a token, or "".
func (c *Checker) classifyCode(core string) string {
	if len(core) < 2 || strings.IndexFunc(core, unicode.IsLetter) < 0 {
		return ""
	}
	switch {
	case flagRe.MatchString(core) && !suffixMentions[core]:
		return "flag"
	case urlRe.MatchString(core):
		return "url"
	case pathLineRe.MatchString(core):
		return "path"
	// a path has a dot, a leading "/", "./", "../" or "~/", or a trailing "/";
	// "and/or" and "may/might/should" are word lists, not paths
	case strings.Contains(core, "/") && pathRe.MatchString(core) &&
		(strings.ContainsRune(core[1:], '.') || strings.HasPrefix(core, "/") || strings.HasPrefix(core, ".") ||
			strings.HasPrefix(core, "~/") || strings.HasSuffix(core, "/")):
		return "path"
	case fileRe.MatchString(core) || dotfileRe.MatchString(core):
		return "file"
	}
	base := strings.TrimSuffix(core, "()")
	switch {
	case dottedRe.MatchString(base):
		// e.g / i.e / a.m / Ph.D are not identifiers. A one-letter part is accepted only next
		// to a part of 3+ characters or before "()": t.Run, r.Body, w.Write(), t.Go().
		short, long := false, false
		for _, seg := range strings.Split(base, ".") {
			short = short || len(seg) < 2
			long = long || len(seg) >= 3
		}
		if short && !long && base == core {
			return ""
		}
		return "dotted"
	case strings.Contains(base, "_") && snakeRe.MatchString(base):
		return "snake_case"
	case mixedRe.MatchString(base) && strings.IndexFunc(base, unicode.IsLower) >= 0 && strings.IndexFunc(base[1:], unicode.IsUpper) >= 0:
		return "mixed_case"
	case base != core && callRe.MatchString(base):
		return "call"
	case acronymRe.MatchString(core) && !labelWords[core]:
		low := strings.ToLower(core)
		_, inForms := c.d.forms[low]
		_, inNA := c.d.nonApproved[low]
		if !inForms && !inNA {
			return "acronym"
		}
	}
	return ""
}

// markAuto replaces code-like tokens with a one-word placeholder and records them.
// Acronyms are only recorded: the word checks already skip all-caps words.
func (c *Checker) markAuto(s string) (string, map[string]string) {
	autos := map[string]string{}
	loud := shouted(s)
	out := chunkRe.ReplaceAllStringFunc(s, func(ch string) string {
		pre, core, suf := splitPunct(ch)
		kind := c.classifyCode(core)
		if kind == "" || (kind == "acronym" && loud) {
			return ch
		}
		a, ok := c.autos[core]
		if !ok {
			a = &AutoHit{Term: core, Kind: kind}
			c.autos[core] = a
			c.autoList = append(c.autoList, a)
		}
		a.Count++
		if kind == "acronym" {
			return ch
		}
		return pre + placeholder("GLOSSARYAUTO", autos, core) + suf
	})
	return out, autos
}

// ---------------------------------------------------------------- checker

// Finding is one structure or grammar finding.
type Finding struct {
	Rule       string `json:"rule"`
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Where      string `json:"where"`
	para, sent int    // for sorting; sent is 0 for a paragraph finding
}

// WordHit is one word that is not approved or not in the dictionary.
type WordHit struct {
	Kind    string `json:"kind"`
	Alts    string `json:"alts"`
	Count   int    `json:"count"`
	Example string `json:"example"`
	Pos     string `json:"pos"`
	Hint    string `json:"hint,omitempty"`
}

// AutoHit is one token accepted by the auto-glossary.
type AutoHit struct {
	Term  string `json:"term"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Checker holds the state of one check run.
type Checker struct {
	d             *Dict
	mode          string
	glossary      map[string]bool
	glossaryMulti []string // multi-word terms, longest first
	findings      []Finding
	wordKeys      []string
	words         map[string]*WordHit
	autoList      []*AutoHit
	autos         map[string]*AutoHit
	codeSpans     map[string]string // placeholder -> `code` span
}

// NewChecker builds a checker. mode is procedural, descriptive or auto; glossary holds the lines of a glossary file.
func NewChecker(d *Dict, mode string, glossary []string) *Checker {
	c := &Checker{d: d, mode: mode, glossary: map[string]bool{}, words: map[string]*WordHit{}, autos: map[string]*AutoHit{}}
	for _, g := range glossary {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == "" || strings.HasPrefix(g, "#") {
			continue
		}
		forms := append([]string{g}, pluralForms(g)...)
		for _, f := range forms {
			c.glossary[f] = true
		}
		if strings.Contains(g, " ") {
			c.glossaryMulti = append(c.glossaryMulti, forms...)
		}
	}
	sort.SliceStable(c.glossaryMulti, func(a, b int) bool { return runeLen(c.glossaryMulti[a]) > runeLen(c.glossaryMulti[b]) })
	return c
}

func (c *Checker) hit(key, kind, alts, example, pos string) {
	h, ok := c.words[key]
	if !ok {
		h = &WordHit{Kind: kind, Alts: alts, Example: example, Pos: pos, Hint: hedgeHints[key]}
		c.words[key] = h
		c.wordKeys = append(c.wordKeys, key)
	}
	h.Count++
}

// lookupBase returns ("approved"|"approved-inflected"|"non_approved"|"glossary"|"unknown", base).
func (c *Checker) lookupBase(t string) (string, string) {
	if c.glossary[t] {
		return "glossary", t
	}
	if f, ok := c.d.forms[t]; ok {
		return "approved", f
	}
	if _, ok := c.d.nonApproved[t]; ok {
		return "non_approved", t
	}
	for _, r := range deinflect {
		if strings.HasSuffix(t, r.suf) && runeLen(t) > len(r.suf)+2 {
			b := t[:len(t)-len(r.suf)] + r.repl
			if f, ok := c.d.forms[b]; ok {
				return "approved-inflected", f
			}
			if _, ok := c.d.nonApproved[b]; ok {
				return "non_approved", b
			}
		}
	}
	return "unknown", t
}

func (c *Checker) approvedHasPos(base string, pos ...string) bool {
	return slices.ContainsFunc(c.d.approved[base], func(e *apEntry) bool { return slices.Contains(pos, e.Pos) })
}

func (c *Checker) isVerb(w string) bool {
	w = strings.ToLower(w)
	if base, ok := c.d.forms[w]; ok {
		w = base
	}
	return c.approvedHasPos(w, "v")
}

func (c *Checker) isFunction(w string) bool {
	return functionWords[w] || c.approvedHasPos(w, "prep", "conj", "art", "pron", "adv")
}

// isAdverb reports an -ly word that the dictionary does not give as a noun or adjective
// ("silently", not "assembly" or "early").
func (c *Checker) isAdverb(t string) bool {
	nounAdj := func(pos string) bool { return pos == "n" || pos == "adj" }
	return strings.HasSuffix(t, "ly") && runeLen(t) > 3 && !c.glossary[t] &&
		!slices.ContainsFunc(c.d.approved[c.d.forms[t]], func(e *apEntry) bool { return nounAdj(e.Pos) }) &&
		!slices.ContainsFunc(c.d.nonApproved[t], func(e *naEntry) bool { return nounAdj(e.Pos) })
}

// ingForm reports an -ing word that can be a verb form: not "thing", "nothing", "string", "bring", "ring".
func ingForm(t string) bool {
	return strings.HasSuffix(t, "ing") && !approvedIng[t] && !strings.HasSuffix(t, "thing") &&
		strings.ContainsAny(strings.TrimSuffix(t, "ing"), "aeiouy")
}

func allVerb(entries []*naEntry) bool {
	return !slices.ContainsFunc(entries, func(e *naEntry) bool { return e.Pos != "v" })
}

func listedTN(entries []*naEntry) bool {
	return slices.ContainsFunc(entries, func(e *naEntry) bool {
		return slices.ContainsFunc(e.UseInstead, func(a string) bool { return strings.HasSuffix(a, "(TN)") })
	})
}

func (c *Checker) sentenceMode(sent string) string {
	if c.mode == "procedural" || c.mode == "descriptive" {
		return c.mode
	}
	// auto: imperative opener, list step, or safety word -> procedural
	first := safetyLabelRe.ReplaceAllString(listMarkerRe.ReplaceAllString(sent, ""), "")
	w := headWordsRe.FindAllString(first, -1)
	if noteRe.MatchString(sent) || len(w) == 0 {
		return "descriptive"
	}
	head := strings.ToLower(w[0])
	// condition-first imperative: "If/When/Before ..., do X"
	if conditionHeads[head] {
		if m := commaWordRe.FindStringSubmatch(first); m != nil && c.isVerb(m[1]) {
			return "procedural"
		}
	}
	if imperativeHeads[head] || (c.isVerb(head) && head != "it" && head != "this" && head != "there") {
		return "procedural"
	}
	return "descriptive"
}

// Check runs every check on text and fills the findings and word hits.
func (c *Checker) Check(text string) {
	text, c.codeSpans = stripCode(text)
	sentNo := 0
	for i, lines := range splitParagraphs(text) {
		pi := i + 1
		if len(lines) == 1 && isHeading(lines[0]) {
			continue
		}
		nList := 0
		for _, l := range lines {
			if listMarkerRe.MatchString(l) {
				nList++
			}
		}
		var sentences []string
		if nList >= max(1, len(lines)/2) {
			for _, l := range lines {
				sentences = append(sentences, splitSentences(l)...)
			}
		} else {
			for k, l := range lines {
				lines[k] = strings.TrimSpace(l)
			}
			if sentences = splitSentences(strings.Join(lines, " ")); len(sentences) > 6 {
				c.findings = append(c.findings, Finding{"6.6", "paragraph", fmt.Sprintf("Paragraph %d has %d sentences (max 6). Divide it.", pi, len(sentences)), fmt.Sprintf("para %d", pi), pi, 0})
			}
		}
		for _, s := range sentences {
			sentNo++
			c.checkSentence(s, pi, sentNo)
		}
	}
}

// contractions returns every word'suffix in s ("don't", "it's", "user's").
func contractions(s string) []string {
	rs := []rune(s)
	var out []string
	for i := 0; i < len(rs); i++ {
		if !isWordRune(rs[i]) || !boundary(rs, i) {
			continue
		}
		e := i
		for e < len(rs) && isWordRune(rs[e]) {
			e++
		}
		for _, suf := range []string{"t", "s", "re", "ve", "ll", "d", "m"} {
			if e < len(rs) && rs[e] == '\'' {
				if end := matchAt(rs, e+1, suf, true); end >= 0 {
					out = append(out, string(rs[i:end]))
					break
				}
			}
		}
		i = e
	}
	return out
}

// tokenize returns the words for the word-level checks, in lower case. A token that starts with
// "\x00" joins noun clusters under its own text: a multi-word glossary term ("<term>"), an
// auto-glossary token, or a capitalized word mid-sentence. Multi-word approved phrases are
// joined with "_".
func (c *Checker) tokenize(s string, autos map[string]string) []string {
	work := parenRe.ReplaceAllString(s, " ")
	work = quoteRe.ReplaceAllString(work, " ")
	work = labelRe.ReplaceAllString(listMarkerRe.ReplaceAllString(work, ""), "")
	for _, g := range c.glossaryMulti {
		work = replaceBounded(work, g, "GLOSSARYTERM")
	}
	for _, mw := range multiWordPhrases {
		work = replaceBounded(work, mw, strings.ReplaceAll(mw, " ", "_"))
	}
	var tokens []string
	for idx, tok := range rawTokRe.FindAllString(work, -1) {
		if tok == "GLOSSARYTERM" {
			tokens = append(tokens, "\x00<term>")
			continue
		}
		if auto, ok := autos[tok]; ok {
			tokens = append(tokens, "\x00"+auto)
			continue
		}
		if strings.Contains(tok, "'") { // contractions are reported separately (4.2); possessives are fine (GR-8)
			lt := strings.ToLower(tok)
			if strings.HasSuffix(lt, "n't") || contractionSet[lt] {
				continue
			}
			if tok, _, _ = strings.Cut(tok, "'"); tok == "" {
				continue
			}
		}
		upper := isUpperWord(tok)
		switch {
		case len(tok) > 1 && upper:
			// abbreviation / quoted label (8.6)
		case idx > 0 && tok[0] >= 'A' && tok[0] <= 'Z' && !upper && !strings.Contains(tok, "_"):
			// capitalized mid-sentence: proper noun or product name (TN category 11)
			tokens = append(tokens, "\x00"+strings.ToLower(tok))
		default:
			tokens = append(tokens, strings.ToLower(tok))
		}
	}
	return tokens
}

func (c *Checker) checkSentence(s string, pi, n int) {
	where := fmt.Sprintf("para %d, sentence %d", pi, n)
	add := func(rule, kind, msg string, args ...any) {
		c.findings = append(c.findings, Finding{rule, kind, fmt.Sprintf(msg, args...), where, pi, n})
	}
	short := restoreCode(s, c.codeSpans)
	if rs := []rune(short); len(rs) > 90 {
		short = string(rs[:87]) + "..."
	}
	s, autos := c.markAuto(strings.ReplaceAll(s, "’", "'")) // a typographic apostrophe is an apostrophe
	mode, limit, rule := "descriptive", 25, "6.3"
	if c.sentenceMode(s) == "procedural" {
		mode, limit, rule = "procedural", 20, "5.1"
	}
	if wc := countWords(s); wc > limit {
		add(rule, "length", "%d words (%s, max %d): \"%s\"", wc, mode, limit, short)
	}
	if strings.Contains(s, ";") {
		add("8.1", "punctuation", "Semicolon: \"%s\"", short)
	}
	for _, m := range contractions(s) {
		if lm := strings.ToLower(m); !strings.HasSuffix(lm, "'s") || contractionSet[lm] { // possessive 's is allowed (GR-8)
			add("4.2", "contraction", "Contraction \"%s\": write the words in full.", m)
		}
	}
	if m := findBounded(s, latinAlts, false); m != "" {
		add("GR-6", "latin", "Latin abbreviation \"%s\": use \"for example\", \"that is\", or rewrite.", m)
	}
	if m := findBounded(s, genderAlts, true); m != "" {
		add("GR-7", "pronoun", "Gender-specific pronoun \"%s\": the rules do not permit he/she. Use \"the operator\", \"you\", \"they\".", m)
	}

	tokens := c.tokenize(s, autos)
	prev := ""
	var runNouns []string
	flushNouns := func() {
		if len(runNouns) >= 4 {
			add("2.1", "noun-cluster", "Possible multi-word noun of %d words: \"%s\". Max 3 — use prepositions or hyphens (2.2).", len(runNouns), strings.Join(runNouns, " "))
		}
		runNouns = nil
	}
	next := ""
	for i, tok := range tokens {
		if next = ""; i+1 < len(tokens) {
			next = tokens[i+1]
		}
		if text, ok := strings.CutPrefix(tok, "\x00"); ok {
			runNouns, prev = append(runNouns, text), strings.ToLower(text)
			continue
		}
		t := strings.ReplaceAll(tok, "_", " ")
		switch {
		case strings.Contains(t, " "): // multi-word approved phrase
			runNouns = nil
		case len(t) < 2:
		case numberWords[t]:
			runNouns = append(runNouns, t)
		case t == "been" || t == "being":
			add("3.4", "tense", "\"%s\": not an approved form of BE (only be, is, are, was, were). Rewrite without it.", t)
		default:
			c.checkWord(t, prev, next, i == 0, short, add, &runNouns, flushNouns)
		}
		prev = t
	}
	flushNouns()
}

// checkWord runs the word-level checks on one token t: approval, -ing, tenses, passive, noun clusters.
func (c *Checker) checkWord(t, prev, next string, first bool, example string, add func(string, string, string, ...any), runNouns *[]string, flushNouns func()) {
	kind, base := c.lookupBase(t)
	entries := c.d.nonApproved[base]
	// part-of-speech heuristic: a word after a determiner/adjective is used as a noun.
	verbOnly := false
	if kind == "non_approved" {
		nounish := c.approvedHasPos(base, "n") || listedTN(entries)
		if determiners[prev] {
			if allVerb(entries) {
				kind = "unknown"
				if nounish {
					kind = "approved"
				}
			}
		} else if !first && !verbSlot[prev] {
			looksVerb := alwaysVerb[base] || hasAnySuffix(base, "ize", "ise", "ify", "ate", "en")
			if !looksVerb && strings.HasPrefix(base, "re") {
				_, looksVerb = c.d.approved[base[2:]]
			}
			verbOnly = allVerb(entries) && !looksVerb && (nounish || !c.isFunction(prev))
		}
	}

	// -ing forms (3.5)
	if kind != "glossary" && ingForm(t) {
		if determiners[prev] || (next != "" && !c.isFunction(next) && !c.isVerb(next) && !beForms[next]) {
			add("3.5?", "ing", "\"-ing\" word \"%s\": permitted only if it is a technical noun or a modifier in one (\"wiring diagram\"). Confirm, else rewrite.", t)
		} else {
			add("3.5", "ing", "\"-ing\" verb form \"%s\": not permitted. Rewrite with a simple tense (\"when you do\", not \"when doing\").", t)
		}
	}

	// complex tenses (3.4) and passive (3.6)
	participle := strings.HasSuffix(t, "ed") || irregularParticiples[t]
	if haveForms[prev] && participle && t != "need" {
		add("3.4", "tense", "\"%s %s\": perfect tense is not approved. Use the simple past or present.", prev, t)
	}
	if beForms[prev] && participle {
		if next == "by" {
			add("3.6", "passive", "Passive voice \"%s %s by\": make the agent the subject.", prev, t)
		} else {
			add("3.6?", "passive", "\"%s %s\": passive voice, or a past participle used as an adjective (permitted, 3.3)? Confirm. In procedures, use the imperative.", prev, t)
		}
	}

	// word approval
	switch kind {
	case "non_approved":
		alts, poses := make([]string, len(entries)), make([]string, len(entries))
		for k, e := range entries {
			if alts[k] = strings.Join(e.UseInstead, ", "); alts[k] == "" {
				alts[k] = e.Note
			}
			poses[k] = e.Pos
		}
		hk := "non_approved"
		if verbOnly {
			hk = "maybe_noun"
		}
		c.hit(base, hk, strings.Join(alts, "; "), example, strings.Join(poses, "/"))
	case "unknown":
		c.hit(base, "unknown", "", example, "")
	}

	// multi-word noun heuristic (2.1): 4+ consecutive noun or adjective words. A function word,
	// a verb (approved, or non-approved and not a possible noun here), or an -ly adverb ends the run.
	if c.isFunction(base) || c.isVerb(base) || (kind == "non_approved" && allVerb(entries) && !verbOnly) || c.isAdverb(t) {
		flushNouns()
	} else {
		*runNouns = append(*runNouns, t)
	}
}

// ---------------------------------------------------------------- output

func (c *Checker) wordsOfKind(kind string) []string {
	var keys []string
	for _, k := range c.wordKeys {
		if c.words[k].Kind == kind {
			keys = append(keys, k)
		}
	}
	sort.SliceStable(keys, func(a, b int) bool { return c.words[keys[a]].Count > c.words[keys[b]].Count })
	return keys
}

var kindOrder = map[string]int{"length": 0, "paragraph": 1, "tense": 2, "passive": 3, "ing": 4, "punctuation": 5, "contraction": 6, "noun-cluster": 7, "latin": 8, "pronoun": 9}

// FormatReport renders the text report.
func (c *Checker) FormatReport() string {
	na, maybe, unk := c.wordsOfKind("non_approved"), c.wordsOfKind("maybe_noun"), c.wordsOfKind("unknown")
	out := []string{"DICTIONARY CHECK", strings.Repeat("=", 60), fmt.Sprintf(
		"Structure/grammar findings: %d   Non-approved words: %d   Not approved as a verb: %d   Words not in dictionary: %d",
		len(c.findings), len(na), len(maybe), len(unk)), ""}
	section := func(title string, lines []string) {
		if len(lines) > 0 {
			out = append(append(append(out, title, strings.Repeat("-", 60)), lines...), "")
		}
	}
	each := func(keys []string, format func(w string, h *WordHit) string) []string {
		var lines []string
		for _, k := range keys {
			if l := format(k, c.words[k]); l != "" {
				lines = append(lines, l)
			}
		}
		return lines
	}
	section("NON-APPROVED WORDS (Rules 1.1–1.3, 9.1) — replace or restructure", each(na, func(w string, h *WordHit) string {
		return fmt.Sprintf("  %s (%s) x%d  ->  %s", w, h.Pos, h.Count, h.Alts)
	}))
	section("NOT APPROVED AS A VERB — fine if used as a noun / technical noun here; otherwise replace", each(maybe, func(w string, h *WordHit) string {
		return fmt.Sprintf("  %s x%d  ->  as a verb use: %s", w, h.Count, h.Alts)
	}))
	if len(unk) > 0 {
		section("WORDS NOT IN THE DICTIONARY — each must be a technical noun/verb (Rules 1.5, 1.12) or be replaced", []string{
			"  " + strings.Join(each(unk, func(w string, h *WordHit) string { return fmt.Sprintf("%s x%d", w, h.Count) }), ", "),
			"  (Add legitimate technical terms to a glossary file and pass --glossary to silence them.)"})
	}
	section("HEDGE WORDS — keep the strength of the claim; never change a possibility into a fact", each(slices.Concat(na, maybe, unk), func(w string, h *WordHit) string {
		if h.Hint == "" {
			return ""
		}
		return fmt.Sprintf("  %s x%d  ->  %s", w, h.Count, h.Hint)
	}))
	var lines []string
	for _, a := range c.autoList {
		lines = append(lines, fmt.Sprintf("  %s (%s) x%d", a.Term, a.Kind, a.Count))
	}
	section("AUTO-GLOSSARY — code-like tokens accepted as technical nouns (Rules 1.5, 8.6); check the list", lines)
	f := slices.Clone(c.findings)
	sort.SliceStable(f, func(a, b int) bool {
		if oa, ob := kindOrder[f[a].Kind], kindOrder[f[b].Kind]; oa != ob {
			return oa < ob
		}
		return f[a].para < f[b].para || (f[a].para == f[b].para && f[a].sent < f[b].sent)
	})
	lines = nil
	for _, x := range f {
		lines = append(lines, fmt.Sprintf("  [%s] %s   (%s)", x.Rule, x.Message, x.Where))
	}
	section("STRUCTURE AND GRAMMAR", lines)
	if len(c.findings)+len(na)+len(maybe)+len(unk) == 0 {
		out = append(out, "No findings. The text passes the mechanical checks. Still read it against the rules that no script can check (one topic per sentence, condition first, notes vs. instructions, approved meaning of each word).")
	}
	return strings.Join(out, "\n")
}

// orderedWords marshals the word hits as a JSON object in first-seen order.
type orderedWords struct{ c *Checker }

func (o orderedWords) MarshalJSON() ([]byte, error) {
	b := []byte{'{'}
	for i, k := range o.c.wordKeys {
		if i > 0 {
			b = append(b, ',')
		}
		key, _ := marshalJSON(k, "")
		val, _ := marshalJSON(o.c.words[k], "")
		b = append(append(append(b, key...), ':'), val...)
	}
	return append(b, '}'), nil
}

// marshalJSON is json.MarshalIndent without HTML escaping.
func marshalJSON(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	err := enc.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), err
}

// FormatJSON renders the machine-readable report.
func (c *Checker) FormatJSON() string {
	out, _ := marshalJSON(struct {
		Findings []Finding    `json:"findings"`
		Words    orderedWords `json:"words"`
		Auto     []*AutoHit   `json:"auto_glossary"`
	}{append([]Finding{}, c.findings...), orderedWords{c}, append([]*AutoHit{}, c.autoList...)}, " ")
	return string(out)
}

// ---------------------------------------------------------------- lookup

// doLookup prints the dictionary entries of each word. Examples are printed only when the
// dictionary has them.
func doLookup(d *Dict, words []string, out io.Writer) {
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		base, ok := d.forms[w]
		if !ok {
			base = w
		}
		fmt.Fprintf(out, "== %s ==\n", w)
		hit := false
		for _, e := range d.approved[base] {
			hit = true
			fmt.Fprintf(out, "  APPROVED  %s (%s): %s\n", e.Word, e.Pos, strings.Join(e.Meaning, "; "))
			for _, f := range [][2]string{{"forms", e.Forms}, {"note", e.Note}, {"for other meanings use", e.ForOther}} {
				if f[1] != "" {
					fmt.Fprintf(out, "    %s: %s\n", f[0], f[1])
				}
			}
			for _, ex := range e.Examples[:min(2, len(e.Examples))] {
				fmt.Fprintf(out, "    e.g. %s\n", ex)
			}
		}
		na := d.nonApproved[w]
		if base != w {
			na = append(slices.Clone(na), d.nonApproved[base]...)
		}
		for _, e := range na {
			hit = true
			fmt.Fprintf(out, "  NOT APPROVED  %s (%s)  ->  %s %s\n", e.Word, e.Pos, strings.Join(e.UseInstead, ", "), e.Note)
			limit := len(e.WrongExample)
			if limit == 0 {
				limit = 3
			}
			for k, ex := range e.Example[:min(limit, len(e.Example))] {
				fmt.Fprintf(out, "    Example: %s\n", ex)
				if k < len(e.WrongExample) && e.WrongExample[k] != "" {
					fmt.Fprintf(out, "    not: %s\n", e.WrongExample[k])
				}
			}
		}
		for _, r := range deinflect[:7] { // "removing" -> see "remove"; "thing" is too short to be "the"+"ing"
			if !hit && strings.HasSuffix(w, r.suf) && runeLen(w) > len(r.suf)+2 {
				if b := w[:len(w)-len(r.suf)] + r.repl; d.approved[b] != nil || d.nonApproved[b] != nil {
					fmt.Fprintf(out, "  (see \"%s\")\n", b)
					doLookup(d, []string{b}, out)
					hit = true
				}
			}
		}
		if !hit {
			fmt.Fprintln(out, "  Not in the dictionary. Permitted only as a technical noun (Rule 1.5) or technical verb (Rule 1.12), otherwise replace it.")
		}
		if hint, ok := hedgeHints[w]; ok {
			fmt.Fprintf(out, "  keep the strength: %s\n", hint)
		}
		fmt.Fprintln(out)
	}
}

// ---------------------------------------------------------------- command line

type cliArgs struct {
	mode, glossary    string
	json, lookup      bool
	noDefaultGlossary bool
	args              []string // FILE, or the words for --lookup
}

// parseArgs accepts flags before or after the positional arguments.
func parseArgs(args []string) (cliArgs, error) {
	var a cliArgs
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.mode, "mode", "auto", "")
	fs.StringVar(&a.glossary, "glossary", "", "")
	fs.BoolVar(&a.json, "json", false, "")
	fs.BoolVar(&a.lookup, "lookup", false, "")
	fs.BoolVar(&a.noDefaultGlossary, "no-default-glossary", false, "")
	for {
		if err := fs.Parse(args); err != nil {
			return a, err
		}
		if fs.NArg() == 0 {
			break
		}
		a.args, args = append(a.args, fs.Arg(0)), fs.Args()[1:]
	}
	switch {
	case a.mode != "procedural" && a.mode != "descriptive" && a.mode != "auto":
		return a, fmt.Errorf("--mode must be procedural, descriptive or auto, not %q", a.mode)
	case a.lookup && len(a.args) == 0:
		return a, errors.New("--lookup needs at least one word")
	case !a.lookup && len(a.args) > 1:
		return a, fmt.Errorf("more than one file: %s", strings.Join(a.args, " "))
	}
	return a, nil
}

func readText(path string, stdin io.Reader) (string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n"), err
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usageText)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "check: %v\nRun with -h for help.\n", err)
		return 2
	}
	d, err := loadDict()
	if err != nil {
		fmt.Fprintf(stderr, "check: dictionary: %v\n", err)
		return 1
	}
	if a.lookup {
		doLookup(d, a.args, stdout)
		return 0
	}
	if len(a.args) == 0 {
		fmt.Fprint(stdout, usageText)
		return 1
	}
	text, err := readText(a.args[0], stdin)
	var glossary string
	if err == nil && a.glossary != "" {
		glossary, err = readText(a.glossary, nil)
	}
	if err != nil {
		fmt.Fprintf(stderr, "check: %v\n", err)
		return 1
	}
	if !a.noDefaultGlossary {
		glossary = defaultGlossary + "\n" + glossary
	}
	c := NewChecker(d, a.mode, strings.Split(glossary, "\n"))
	c.Check(text)
	if a.json {
		fmt.Fprintln(stdout, c.FormatJSON())
	} else {
		fmt.Fprintln(stdout, c.FormatReport())
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
