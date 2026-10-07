package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustDict(t *testing.T) *Dict {
	t.Helper()
	d, err := loadDict()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func check(t *testing.T, glossary []string, text string) *Checker {
	t.Helper()
	c := NewChecker(mustDict(t), "auto", glossary)
	c.Check(text)
	return c
}

func kinds(c *Checker) []string {
	var out []string
	for _, f := range c.findings {
		out = append(out, f.Rule+" "+f.Kind+": "+f.Message)
	}
	return out
}

func hasFinding(c *Checker, rule string) bool {
	for _, f := range c.findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func findingsOf(c *Checker, rule string) []string {
	var out []string
	for _, f := range c.findings {
		if f.Rule == rule {
			out = append(out, f.Message)
		}
	}
	return out
}

func runCLI(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return out.String() + errb.String(), code
}

func TestLookupBase(t *testing.T) {
	c := NewChecker(mustDict(t), "auto", []string{"breaker", "flux capacitor", "# comment", ""})
	tests := []struct {
		name, word, kind, base string
	}{
		{"approved base", "remove", "approved", "remove"},
		{"approved listed form", "removed", "approved", "remove"},
		{"approved plural", "tools", "approved", "tool"},
		{"irregular be", "were", "approved", "be"},
		{"inflected by suffix", "removing", "approved-inflected", "remove"},
		{"non-approved", "ensure", "non_approved", "ensure"},
		{"non-approved inflected", "ensures", "non_approved", "ensure"},
		{"unknown", "xyzzy", "unknown", "xyzzy"},
		{"glossary", "breaker", "glossary", "breaker"},
		{"glossary plural", "breakers", "glossary", "breakers"},
		{"multi-word approved", "make sure", "approved", "make sure"},
		{"multi-word inflected", "made sure", "approved", "make sure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, base := c.lookupBase(tt.word)
			if kind != tt.kind || base != tt.base {
				t.Errorf("lookupBase(%q) = (%q, %q), want (%q, %q)", tt.word, kind, base, tt.kind, tt.base)
			}
		})
	}
	if want := []string{"flux capacitors", "flux capacitor"}; !reflect.DeepEqual(c.glossaryMulti, want) {
		t.Errorf("glossaryMulti = %v, want %v (longest first)", c.glossaryMulti, want)
	}
}

func TestWordHits(t *testing.T) {
	tests := []struct {
		name     string
		glossary []string
		text     string
		want     map[string]string // word -> kind
	}{
		{"non-approved and unknown", nil, "Ensure that the xyzzy is clean.", map[string]string{"ensure": "non_approved", "xyzzy": "unknown"}},
		{"approved only", nil, "Remove the cover.", map[string]string{}},
		{"verb used as noun after determiner", nil, "Do the test.", map[string]string{}},
		{"glossary word", []string{"xyzzy"}, "Clean the xyzzy.", map[string]string{}},
		{"multi-word glossary", []string{"flux capacitor"}, "Clean the flux capacitor.", map[string]string{}},
		{"multi-word without glossary", nil, "Clean the flux capacitor.", map[string]string{"flux": "unknown", "capacitor": "unknown"}},
		{"proper noun mid-sentence is skipped", nil, "Connect the Zorbatron.", map[string]string{}},
		{"all-caps abbreviation is skipped", nil, "Connect the ZQX unit.", map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, tt.glossary, tt.text)
			got := map[string]string{}
			for _, k := range c.wordKeys {
				got[k] = c.words[k].Kind
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("word hits = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		rule, sentence string
		want           int
	}{
		{"plain", "Remove the cover.", 3},
		{"8.4 step number not counted", "1. Remove the cover.", 3},
		{"8.4 list bullet not counted", "- Remove the cover.", 3},
		{"8.4 safety label not counted", "WARNING: Remove the cover.", 3},
		{"8.5 parenthesis is one word", "Remove the cover (see the note below).", 4},
		{"8.6 number and unit", "Drill a 10 mm hole.", 4},
		{"8.6 number and unit, no space", "Drill a 10mm hole.", 4},
		{"8.6 temperature", "Heat it to 25 °C.", 4},
		{"8.6 percent then word", "Fill 50% of it.", 4},
		{"8.6 identifier", "Install the A320-200 unit.", 4},
		{"8.6 abbreviation", "Connect the AB/CD-12 cable.", 4},
		{"8.6 quoted text", `Set the switch to "SYSTEM ON NOW".`, 5},
		{"8.6 curly quoted text", "Push “START TEST NOW”.", 2},
		{"8.6 decimal number", "Torque to 2.5 psi.", 3},
		{"8.6 units are lower case only", "Torque to 2.5 kPa.", 4},
		{"8.6 ohm sign", "Set it to 5 Ω now.", 5},
		{"8.7 hyphenated word", "Do a self-test.", 3},
		{"possessive of abbreviation counts twice", "Read ABC's value.", 4},
		{"Unicode letters", "Café naïve résumé.", 3},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			if got := countWords(tt.sentence); got != tt.want {
				t.Errorf("countWords(%q) = %d, want %d", tt.sentence, got, tt.want)
			}
		})
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name, block string
		want        []string
	}{
		{"two sentences", "Remove the cover. Clean it.", []string{"Remove the cover.", "Clean it."}},
		{"lower case after a period does not split", "Use it. then go.", []string{"Use it. then go."}},
		{"protected abbreviation", "Use a tool, e.g. A wrench.", []string{"Use a tool, e.g. A wrench."}},
		{"protected abbreviation in parenthesis", "Use a tool (e.g. A wrench).", []string{"Use a tool (e.g. A wrench)."}},
		{"protected abbreviation at start", "No. 5 is open.", []string{"No. 5 is open."}},
		{"decimal number", "Set 1.5 V. Then stop.", []string{"Set 1.5 V.", "Then stop."}},
		{"colon ends a list intro (8.4)", "Do these steps: Remove the cover.", []string{"Do these steps:", "Remove the cover."}},
		{"question and exclamation", "Is it on? Stop it! Go.", []string{"Is it on?", "Stop it!", "Go."}},
		{"opening quote or parenthesis", `Stop. "Start" it. (Note) here.`, []string{"Stop.", `"Start" it.`, "(Note) here."}},
		{"number after period", "Do step 3. 4 bolts remain.", []string{"Do step 3.", "4 bolts remain."}},
		{"empty", "   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitSentences(tt.block); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitSentences(%q) = %q, want %q", tt.block, got, tt.want)
			}
		})
	}
}

func TestSentenceFindings(t *testing.T) {
	tests := []struct {
		name, text string
		want       string // rule that must be present; "" = no findings at all
	}{
		{"-ing verb form", "When you are doing it, stop.", "3.5"},
		{"-ing word that may be a noun", "Examine the wiring diagram.", "3.5?"},
		{"approved -ing form", "Do the servicing.", ""},
		{"perfect tense", "The pump has failed.", "3.4"},
		{"been", "It has been done.", "3.4"},
		{"passive with agent", "The valve is removed by the operator.", "3.6"},
		{"passive or adjective", "The valve is removed.", "3.6?"},
		{"semicolon", "Stop the pump; open the valve.", "8.1"},
		{"contraction n't", "Don't open the valve.", "4.2"},
		{"contraction it's", "It's the valve.", "4.2"},
		{"possessive is permitted", "Open the operator's valve.", ""},
		{"Latin e.g.", "Use a tool, e.g. a wrench.", "GR-6"},
		{"Latin etc", "Use a tool, a wrench etc.", "GR-6"},
		{"gendered pronoun", "He opens the valve.", "GR-7"},
		{"gendered pronoun, other case", "Give it to HER.", "GR-7"},
		{"noun cluster", "Examine the fuel pump pressure sensor connector.", "2.1"},
		{"procedural length", "Remove the cover and the filter and the tool and the unit and the cover and the filter and the tool and the unit.", "5.1"},
		{"descriptive length", "The pump is a unit that is in the system and it is in the area of the left wing and it is near the area of the right wing too.", "6.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, nil, tt.text)
			if tt.want == "" {
				if len(c.findings) != 0 {
					t.Errorf("want no findings, got %v", kinds(c))
				}
				return
			}
			if !hasFinding(c, tt.want) {
				t.Errorf("want rule %s, got %v", tt.want, kinds(c))
			}
		})
	}
}

func TestParagraphLength(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"seven sentences", "Remove it. Clean it. Install it. Examine it. Measure it. Tighten it. Record it.", true},
		{"seven short sentences", "One. Two. Three. Four. Five. Six. Seven.", true},
		{"six sentences", "Remove it. Clean it. Install it. Examine it. Measure it. Tighten it.", false},
		{"list items are not a paragraph", "1. One.\n2. Two.\n3. Three.\n4. Four.\n5. Five.\n6. Six.\n7. Seven.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasFinding(check(t, nil, tt.text), "6.6"); got != tt.want {
				t.Errorf("6.6 finding = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeadingsSkipped(t *testing.T) {
	c := check(t, nil, "# Remove Doing Ensure\n\nInstallation Of The Unit\n\nRemove the cover.")
	if len(c.findings) != 0 || len(c.wordKeys) != 0 {
		t.Errorf("headings must be skipped, got findings %v words %v", kinds(c), c.wordKeys)
	}
}

func TestLookupOutput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		notWant []string
	}{
		{"approved word", []string{"--lookup", "remove"}, []string{"== remove ==", "  APPROVED  REMOVE (v):"}, nil},
		{"non-approved word", []string{"--lookup", "ensure"}, []string{"  NOT APPROVED  ensure (v)  ->  ", "    Example: "}, nil},
		{"inflected form is resolved", []string{"--lookup", "ensures"}, []string{"  (see \"ensure\")", "== ensure =="}, nil},
		{"unknown word", []string{"--lookup", "xyzzy"}, []string{"  Not in the dictionary."}, nil},
		{"several words, case and spaces", []string{"--lookup", "  MAY ", "valve"}, []string{"== may ==", "== valve =="}, nil},
		{"flags after the words", []string{"--lookup", "may", "--json"}, []string{"== may =="}, nil},
		{"hedge hint", []string{"--lookup", "may"}, []string{"  keep the strength: POSSIBLY"}, nil},
		{"hedge hint for an unknown word", []string{"--lookup", "probably"}, []string{"Not in the dictionary", "IT IS VERY POSSIBLE THAT"}, nil},
		{"short word is not de-inflected", []string{"--lookup", "thing"}, []string{"Not in the dictionary"}, []string{"(see \"the\")"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := runCLI(t, "", tt.args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q:\n%s", w, out)
				}
			}
		})
	}
}

// TestDictionaryShape: the generated dictionary has extra *_raw / *_table fields and empty
// approved examples. The loader ignores the extra fields, and --lookup prints no example lines
// when there are none.
func TestDictionaryShape(t *testing.T) {
	d, err := parseDict([]byte(`{
		"source": "x", "approved_table": {"preamble": "p"},
		"approved": [{"word": "VALVE", "pos": "n", "meaning": ["A device"], "forms": "", "examples": [], "meaning_raw": "A device", "notes_raw": ""},
		             {"word": "OPEN", "pos": "v", "meaning": ["To move"], "forms": "OPENS, OPENED", "examples": ["OPEN THE VALVE."]}],
		"non_approved": [{"word": "ensure", "pos": "v", "use_instead": ["MAKE SURE (v)"], "ste_example": [], "non_ste_example": [], "use_instead_raw": "MAKE SURE (v)"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		word    string
		want    []string
		notWant []string
	}{
		{"valve", []string{"APPROVED  VALVE (n): A device"}, []string{"e.g.", "forms:"}},
		{"opened", []string{"APPROVED  OPEN (v)", "forms: OPENS, OPENED", "e.g. OPEN THE VALVE."}, nil},
		{"ensure", []string{"NOT APPROVED  ensure (v)  ->  MAKE SURE (v)"}, []string{"Example:", "not:"}},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			var out bytes.Buffer
			doLookup(d, []string{tt.word}, &out)
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out.String(), w) {
					t.Errorf("output has %q:\n%s", w, out.String())
				}
			}
		})
	}
	t.Run("plural form index", func(t *testing.T) {
		if d.forms["valves"] != "valve" || d.forms["opened"] != "open" {
			t.Errorf("forms = %v", d.forms)
		}
	})
	t.Run("embedded dictionary loads", func(t *testing.T) {
		d := mustDict(t)
		if len(d.approved) < 800 || len(d.nonApproved) < 1000 {
			t.Errorf("approved %d, non-approved %d", len(d.approved), len(d.nonApproved))
		}
	})
}

func TestTextHelpers(t *testing.T) {
	t.Run("title case", func(t *testing.T) {
		for in, want := range map[string]bool{"Hello World": true, "Hello world": false, "Don't": false, "1st": false, "Installation Of The Unit": true, "CODESPANAAA": false} {
			if got := isTitleCase(in); got != want {
				t.Errorf("isTitleCase(%q) = %v, want %v", in, got, want)
			}
		}
	})
	t.Run("upper word", func(t *testing.T) {
		for in, want := range map[string]bool{"ABC": true, "A-B": true, "AbC": false, "--": false} {
			if got := isUpperWord(in); got != want {
				t.Errorf("isUpperWord(%q) = %v, want %v", in, got, want)
			}
		}
	})
}

func TestCodeSkip(t *testing.T) {
	tests := []struct {
		name, text string
		words      int  // number of word hits
		gender     bool // GR-7 finding present
	}{
		{"inline span not checked", "Use `he utilizes xyzzy` with the tool.", 0, false},
		{"fenced block removed", "Stop it.\n```\nhe utilizes xyzzy\n```\nUse the tool.", 0, false},
		{"tilde fence", "~~~\nhe utilizes xyzzy\n~~~", 0, false},
		{"double backtick span", "Use ``a ` b he`` with the tool.", 0, false},
		{"unclosed backtick stays text", "Use `he with the tool.", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, nil, tt.text)
			if len(c.wordKeys) != tt.words {
				t.Errorf("word hits %v, want %d", c.wordKeys, tt.words)
			}
			if got := hasFinding(c, "GR-7"); got != tt.gender {
				t.Errorf("GR-7 = %v, want %v (%v)", got, tt.gender, kinds(c))
			}
		})
	}
	t.Run("span counts as one word (8.6)", func(t *testing.T) {
		text, spans := stripCode("Run `go test -race -count=1 ./... ./x` now.")
		if got := countWords(text); got != 3 {
			t.Errorf("countWords(%q) = %d, want 3", text, got)
		}
		if got := restoreCode(text, spans); got != "Run `go test -race -count=1 ./... ./x` now." {
			t.Errorf("restoreCode = %q", got)
		}
	})
	t.Run("example quotes the original span", func(t *testing.T) {
		c := check(t, nil, "Run `make build` now.")
		if ex := c.words["run"].Example; ex != "Run `make build` now." {
			t.Errorf("example = %q", ex)
		}
	})
}

func TestAutoGlossaryClassify(t *testing.T) {
	c := NewChecker(mustDict(t), "auto", nil)
	tests := []struct{ token, kind string }{
		{"internal/chat/report.go", "path"},
		{"a/b.c", "path"},
		{"/etc/hosts", "path"},
		{"x.go:12", "path"},
		{"report.go:120:5", "path"},
		{"https://example.com/x", "url"},
		{"main.go", "file"},
		{".env", "file"},
		{"snake_case", "snake_case"},
		{"__init__", "snake_case"},
		{"MAX_RETRIES", "snake_case"},
		{"camelCase", "mixed_case"},
		{"PascalCase", "mixed_case"},
		{"gRPC", "mixed_case"},
		{"APIs", "mixed_case"},
		{"fmt.Println", "dotted"},
		{"os.Exit", "dotted"},
		{"fmt.Println()", "dotted"},
		{"t.Run()", "dotted"},
		{"t.Run", "dotted"},
		{"r.Body", "dotted"},
		{"t.Go()", "dotted"},
		{"run()", "call"},
		{"--no-cache", "flag"},
		{"--mode=auto", "flag"},
		{"-race", "flag"},
		{"API", "acronym"},
		{"CI", "acronym"},
		{"HTTP", "acronym"},
		{"EC2", "acronym"},
		{"cmd/server/", "path"},
		{"./scripts", "path"},
		// not code
		{"and/or", ""},
		{"may/might/likely/should", ""},
		{"internal/chat", ""},
		{"-ing", ""},
		{"km/h", ""},
		{"1/2", ""},
		{"e.g", ""},
		{"i.e", ""},
		{"a.m", ""},
		{"Ph.D", ""},
		{"t.Go", ""},
		{"Hello", ""},
		{"hello", ""},
		{"NOTE", ""},
		{"CAUTION", ""},
		{"SET", ""},
		{"OFF", ""},
		{"ABCDEFG", ""},
		{"x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			if got := c.classifyCode(tt.token); got != tt.kind {
				t.Errorf("classifyCode(%q) = %q, want %q", tt.token, got, tt.kind)
			}
		})
	}
}

func TestSplitPunct(t *testing.T) {
	tests := []struct{ chunk, pre, core, suf string }{
		{"fmt.Println,", "", "fmt.Println", ","},
		{"(fmt.Println())", "(", "fmt.Println()", ")"},
		{"x.go:12.", "", "x.go:12", "."},
		{"“gRPC”", "“", "gRPC", "”"},
		{"API's", "", "API", "'s"},
		{"API's.", "", "API", "'s."},
	}
	for _, tt := range tests {
		t.Run(tt.chunk, func(t *testing.T) {
			pre, core, suf := splitPunct(tt.chunk)
			if pre != tt.pre || core != tt.core || suf != tt.suf {
				t.Errorf("splitPunct(%q) = (%q, %q, %q), want (%q, %q, %q)", tt.chunk, pre, core, suf, tt.pre, tt.core, tt.suf)
			}
		})
	}
}

func TestAutoGlossaryCheck(t *testing.T) {
	text := "The handler in internal/chat/report.go:120 calls fmt.Println and parse_tool_result. Use --no-cache with gRPC and the API."
	c := check(t, nil, text)
	for _, w := range []string{"fmt", "grpc", "no-cache", "chat"} {
		if _, ok := c.words[w]; ok {
			t.Errorf("%q must not be a word hit", w)
		}
	}
	var terms []string
	for _, a := range c.autoList {
		terms = append(terms, a.Term+"="+a.Kind)
	}
	want := []string{"internal/chat/report.go:120=path", "fmt.Println=dotted", "parse_tool_result=snake_case", "--no-cache=flag", "gRPC=mixed_case", "API=acronym"}
	if !reflect.DeepEqual(terms, want) {
		t.Errorf("auto_glossary = %v, want %v", terms, want)
	}
	t.Run("capitals in a shouted sentence are not acronyms", func(t *testing.T) {
		c := check(t, nil, "REPLACE THE GASKET ON THE API UNIT. Read the API page.")
		if a := c.autos["API"]; a == nil || a.Count != 1 {
			t.Errorf("API = %+v, want count 1 (only from the normal sentence)", a)
		}
		if _, ok := c.autos["GASKET"]; ok {
			t.Error("GASKET listed as an acronym")
		}
	})
	t.Run("token counts as one word", func(t *testing.T) {
		s, _ := c.markAuto("Read internal/chat/report.go:120 now.")
		if got := countWords(s); got != 3 {
			t.Errorf("countWords(%q) = %d, want 3", s, got)
		}
	})
	t.Run("token joins a noun cluster with its own text", func(t *testing.T) {
		c := check(t, nil, "Examine the fmt.Println output buffer size.")
		if got := findingsOf(c, "2.1"); len(got) != 1 || !strings.Contains(got[0], "fmt.Println output buffer size") {
			t.Errorf("want a 2.1 finding with the token text, got %v", kinds(c))
		}
	})
}

func TestHedgeHints(t *testing.T) {
	text := "It may fail. It might fail. It is likely to fail. It probably fails. Perhaps it fails. It is unlikely to fail. A probable cause. You should stop."
	c := check(t, nil, text)
	want := map[string]string{
		"may":      "POSSIBLY",
		"might":    "POSSIBLY",
		"perhaps":  "POSSIBLY",
		"likely":   "IT IS VERY POSSIBLE THAT",
		"probably": "IT IS VERY POSSIBLE THAT",
		"probable": "IT IS VERY POSSIBLE THAT",
		"unlikely": "THE RISK IS SMALL",
		"should":   "WE RECOMMEND THAT",
	}
	for w, sub := range want {
		t.Run(w, func(t *testing.T) {
			h, ok := c.words[w]
			if !ok {
				t.Fatalf("%q is not a word hit", w)
			}
			if !strings.Contains(h.Hint, sub) {
				t.Errorf("hint for %q = %q, want it to contain %q", w, h.Hint, sub)
			}
		})
	}
	t.Run("should names both senses", func(t *testing.T) {
		if h := c.words["should"].Hint; !strings.Contains(h, "MUST") || !strings.Contains(h, "JUDGE") {
			t.Errorf("should hint = %q", h)
		}
	})
	t.Run("hints only on hedge words", func(t *testing.T) {
		if h := c.words["fail"]; h != nil && h.Hint != "" {
			t.Errorf("fail has a hint: %q", h.Hint)
		}
	})
	t.Run("report section", func(t *testing.T) {
		if r := c.FormatReport(); !strings.Contains(r, "HEDGE WORDS") {
			t.Errorf("report lacks the hedge section:\n%s", r)
		}
	})
}

// TestHedgeHintWordsApproved makes sure every capitalized word in a hint is an approved dictionary word.
func TestHedgeHintWordsApproved(t *testing.T) {
	d := mustDict(t)
	for w, hint := range hedgeHints {
		for _, tok := range strings.FieldsFunc(hint, func(r rune) bool { return !(r >= 'A' && r <= 'Z') }) {
			if len(tok) < 2 || tok == "JUDGE" {
				continue
			}
			low := strings.ToLower(tok)
			base, ok := d.forms[low]
			if !ok {
				t.Errorf("hint for %q uses %q, which is not an approved word", w, tok)
				continue
			}
			if _, ok := d.approved[base]; !ok {
				t.Errorf("hint for %q uses %q (base %q), which is not approved", w, tok, base)
			}
		}
	}
}

func TestJSONShape(t *testing.T) {
	out := check(t, nil, "Ensure the xyzzy <is> clean & dry; use fmt.Println. It may fail.").FormatJSON()
	t.Run("layout", func(t *testing.T) {
		if !strings.HasPrefix(out, "{\n \"findings\": [\n  {\n   \"rule\": ") {
			t.Errorf("unexpected layout:\n%s", out)
		}
		if strings.Contains(out, `\u003c`) || strings.Contains(out, `\u0026`) || !strings.Contains(out, "<is> clean & dry") {
			t.Errorf("HTML characters are escaped:\n%s", out)
		}
		if strings.Index(out, `"kind": "non_approved"`) > strings.Index(out, `"alts": "MAKE SURE`) {
			t.Error("word entry keys out of order")
		}
		if strings.Index(out, `"ensure": {`) > strings.Index(out, `"xyzzy": {`) {
			t.Error("words are not in first-seen order")
		}
	})
	t.Run("keys", func(t *testing.T) {
		var v struct {
			Findings     []map[string]any `json:"findings"`
			AutoGlossary []struct {
				Term, Kind string
				Count      int
			} `json:"auto_glossary"`
			Words map[string]struct{ Kind, Hint string } `json:"words"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Findings) == 0 || len(v.Findings[0]) != 4 {
			t.Errorf("findings = %v, want rule/kind/message/where", v.Findings)
		}
		if len(v.AutoGlossary) != 1 || v.AutoGlossary[0].Term != "fmt.Println" {
			t.Errorf("auto_glossary = %+v", v.AutoGlossary)
		}
		if v.Words["may"].Hint == "" || v.Words["ensure"].Kind != "non_approved" {
			t.Errorf("words = %+v", v.Words)
		}
	})
	t.Run("empty lists", func(t *testing.T) {
		if out := check(t, nil, "Remove the cover.").FormatJSON(); out != "{\n \"findings\": [],\n \"words\": {},\n \"auto_glossary\": []\n}" {
			t.Errorf("got %s", out)
		}
	})
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliArgs
		wantErr bool
	}{
		{"flags after file", []string{"f.md", "--mode", "procedural", "--json"}, cliArgs{args: []string{"f.md"}, mode: "procedural", json: true}, false},
		{"flags before file", []string{"--json", "--glossary=g.txt", "f.md"}, cliArgs{args: []string{"f.md"}, mode: "auto", glossary: "g.txt", json: true}, false},
		{"stdin", []string{"-"}, cliArgs{args: []string{"-"}, mode: "auto"}, false},
		{"lookup many", []string{"--lookup", "may", "might", "--json"}, cliArgs{mode: "auto", args: []string{"may", "might"}, lookup: true, json: true}, false},
		{"bad mode", []string{"f", "--mode", "fast"}, cliArgs{}, true},
		{"lookup without words", []string{"--lookup", "--json"}, cliArgs{}, true},
		{"unknown flag", []string{"f", "--fast"}, cliArgs{}, true},
		{"removed flag", []string{"f", "--no-code-skip"}, cliArgs{}, true},
		{"two files", []string{"a", "b"}, cliArgs{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseArgs = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"no file prints help", nil, 1},
		{"help", []string{"-h"}, 0},
		{"long help", []string{"--help"}, 0},
		{"bad flag", []string{"--nope"}, 2},
		{"missing file", []string{"does-not-exist.md"}, 1},
		{"missing glossary", []string{"-", "--glossary", "does-not-exist.txt"}, 1},
		{"stdin", []string{"-"}, 0},
		{"stdin json", []string{"-", "--json"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, code := runCLI(t, "Remove the cover.", tt.args...); code != tt.code {
				t.Errorf("exit %d, want %d (%s)", code, tt.code, out)
			}
		})
	}
}

// ---------------------------------------------------------------- regressions (code-review findings 1-9)

// Finding 1: a possessive 's before a contraction hid the contraction.
// Finding 6: a typographic apostrophe (U+2019) was not an apostrophe.
func TestRegressionContractions(t *testing.T) {
	tests := []struct {
		name, text string
		want       []string // the contractions in the 4.2 findings
		notWord    string   // must not be a word hit
	}{
		{"possessive then contraction", "The user's file doesn't open.", []string{"doesn't"}, ""},
		{"two contractions", "It doesn't open and it won't close.", []string{"doesn't", "won't"}, ""},
		{"possessive then it's", "The user's valve is open and it's hot.", []string{"it's"}, ""},
		{"possessive only", "Open the operator's valve.", nil, ""},
		{"curly don't", "Don’t open the valve.", []string{"Don't"}, "don"},
		{"curly it's", "It’s the valve.", []string{"It's"}, ""},
		{"curly possessive", "Open the operator’s valve.", nil, "operator’s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, nil, tt.text)
			var got []string
			for _, m := range findingsOf(c, "4.2") {
				got = append(got, strings.Split(m, `"`)[1])
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("4.2 contractions = %q, want %q (%v)", got, tt.want, kinds(c))
			}
			if _, ok := c.words[tt.notWord]; tt.notWord != "" && ok {
				t.Errorf("%q is a word hit", tt.notWord)
			}
		})
	}
	t.Run("example keeps the typographic apostrophe", func(t *testing.T) {
		if ex := check(t, nil, "Don’t open the xyzzy.").words["xyzzy"].Example; ex != "Don’t open the xyzzy." {
			t.Errorf("example = %q", ex)
		}
	})
}

// Finding 2: "admin." ends with "min.", so the abbreviation guard joined two sentences.
func TestRegressionAbbreviationBoundary(t *testing.T) {
	tests := []struct {
		block string
		want  int
	}{
		{"Log in as admin. Then restart the server now.", 2},
		{"Set the maximum to vs. Then stop.", 1}, // "vs." alone is still protected
		{"Read the max. Value on the gauge.", 1},
		{"Move it to the Fig. 3 position.", 1},
		{"Use the formax. Then stop.", 2},
		{"Open the No. 2 valve.", 1},
		{"Read the info. Then stop.", 2},
	}
	for _, tt := range tests {
		t.Run(tt.block, func(t *testing.T) {
			if got := splitSentences(tt.block); len(got) != tt.want {
				t.Errorf("splitSentences(%q) = %q, want %d sentences", tt.block, got, tt.want)
			}
		})
	}
}

// Finding 3: a hard-wrapped line that starts with "it. Then" looked like a list item, so the
// paragraph was a list and the 6.6 and sentence-length checks were skipped.
func TestRegressionWrappedProse(t *testing.T) {
	t.Run("list markers", func(t *testing.T) {
		for line, want := range map[string]bool{
			"it. Then go.": false, "the. End.": false, "to) here": false, "Stop. Go.": false,
			"1. Go.": true, "10) Go.": true, "2a. Go.": true, "a) Go.": true, "(b) Go.": true, "iv. Go.": true,
			"- Go.": true, "• Go.": true, "* Go.": true, "– Go.": true,
		} {
			if got := listMarkerRe.MatchString(line); got != want {
				t.Errorf("listMarkerRe.MatchString(%q) = %v, want %v", line, got, want)
			}
		}
	})
	t.Run("paragraph length", func(t *testing.T) {
		c := check(t, nil, "Remove it. Clean it. Install it. Examine\nit. Measure it. Tighten it. Record it.")
		if !hasFinding(c, "6.6") {
			t.Errorf("want 6.6, got %v", kinds(c))
		}
	})
	t.Run("sentence length across the wrap", func(t *testing.T) {
		c := check(t, nil, "The pump is a unit that is in the system and it is in the area of the left wing and it is very near\nit. Then it stops.")
		if !hasFinding(c, "6.3") {
			t.Errorf("want 6.3, got %v", kinds(c))
		}
	})
}

// Finding 4: a fence indented 4+ spaces (in a nested list) and an indented code block were checked as prose.
func TestRegressionIndentedCode(t *testing.T) {
	tests := []struct {
		name, text string
		code       bool // true: "he utilizes xyzzy" is skipped as code
	}{
		{"fence in a nested list", "- Step one:\n\n    ```\n    he utilizes xyzzy\n    ```\n\nThen stop.", true},
		{"tab-indented fence", "1. Step:\n\t~~~\n\the utilizes xyzzy\n\t~~~", true},
		{"indented block after a paragraph", "Run this:\n\n    he utilizes xyzzy\n\n    and more code\n\nThen stop.", true},
		{"indented block at the start", "    he utilizes xyzzy\n\nThen stop.", true},
		{"indented continuation of a list item", "- Step one.\n\n    he utilizes xyzzy.", false},
		{"indented line inside a paragraph", "Read this line\n    he utilizes xyzzy.", false},
		{"indented line after the list ends", "- Step one.\n\nA paragraph.\n\n    he utilizes xyzzy", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, nil, tt.text)
			_, ok := c.words["xyzzy"]
			if ok == tt.code || hasFinding(c, "GR-7") == tt.code {
				t.Errorf("code = %v, want %v (words %v, findings %v)", !ok, tt.code, c.wordKeys, kinds(c))
			}
		})
	}
	t.Run("text after the indented block is checked", func(t *testing.T) {
		if c := check(t, nil, "Run this:\n\n    go test\n\nEnsure it works."); c.words["ensure"] == nil {
			t.Errorf("words = %v", c.wordKeys)
		}
	})
}

// Finding 5: every word that ends in "ing" got a 3.5 finding.
func TestRegressionIngWords(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"thing", false}, {"string", false}, {"nothing", false}, {"anything", false}, {"everything", false},
		{"something", false}, {"bring", false}, {"ring", false}, {"spring", false}, {"king", false}, {"ing", false},
		{"during", false}, {"servicing", false},
		{"doing", true}, {"merging", true}, {"taking", true}, {"according", true}, {"wiring", true}, {"testing", true},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			if got := ingForm(tt.word); got != tt.want {
				t.Errorf("ingForm(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
	t.Run("sentence", func(t *testing.T) {
		if c := check(t, nil, "The thing in the string is nothing, so bring the ring."); hasFinding(c, "3.5") || hasFinding(c, "3.5?") {
			t.Errorf("got %v", kinds(c))
		}
	})
}

// Finding 7: findings were sorted by the "where" text, so "para 10" came before "para 2".
func TestRegressionFindingOrder(t *testing.T) {
	paras := make([]string, 11)
	for i := range paras {
		paras[i] = "Remove the cover."
	}
	paras[1], paras[9] = "Stop the pump; open the valve.", "Stop the fan; open the door."
	paras[10] = "One. Two. Three. Four. Five. Six. Seven. Eight. Nine. Ten; eleven."
	r := check(t, nil, strings.Join(paras, "\n\n")).FormatReport()
	p2, p10, p11 := strings.Index(r, "(para 2, sentence 2)"), strings.Index(r, "(para 10, sentence 10)"), strings.Index(r, "(para 11, sentence 20)")
	if p2 < 0 || p10 < 0 || p11 < 0 || !(p2 < p10 && p10 < p11) {
		t.Errorf("want para 2 < para 10 < para 11 (%d, %d, %d):\n%s", p2, p10, p11, r)
	}
}

// Finding 8: the summary ignored "not approved as a verb" words, and "No findings" was printed with them listed.
func TestRegressionSummaryCountsMaybeNouns(t *testing.T) {
	c := check(t, nil, "The guard fails.")
	if c.words["fail"] == nil || c.words["fail"].Kind != "maybe_noun" {
		t.Fatalf("want fail as maybe_noun, got %v", c.words)
	}
	r := c.FormatReport()
	if strings.Contains(r, "No findings") || !strings.Contains(r, "Not approved as a verb: 1") {
		t.Errorf("report:\n%s", r)
	}
	if r := check(t, nil, "Remove the cover.").FormatReport(); !strings.Contains(r, "No findings") || !strings.Contains(r, "Not approved as a verb: 0") {
		t.Errorf("clean report:\n%s", r)
	}
}

// Finding 9: an adverb and a verb were counted as nouns in a noun cluster ("guard probably fails silently").
func TestRegressionNounCluster(t *testing.T) {
	tests := []struct {
		name, text string
		want       string // the 2.1 cluster, or "" for none
	}{
		{"adverb and verb", "This may have broken the oracle; the guard probably fails silently when the pin is set.", ""},
		{"user example", "The guard probably fails silently.", ""},
		{"modal verb ends the run", "Examine the cable clamp bolt may fail.", ""},
		{"noun cluster", "Examine the fuel pump pressure sensor connector.", "fuel pump pressure sensor connector"},
		{"noun ending in -ly", "Remove the fuel pump assembly bracket.", "fuel pump assembly bracket"},
		{"plural noun that is a non-approved verb", "Remove the four stainless steel pan head machine screws.", "four stainless steel pan head machine screws"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := check(t, nil, tt.text)
			got := ""
			for _, m := range findingsOf(c, "2.1") {
				got = strings.Split(m, `"`)[1]
			}
			if got != tt.want {
				t.Errorf("2.1 cluster = %q, want %q (%v)", got, tt.want, kinds(c))
			}
		})
	}
}
