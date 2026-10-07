package main

// Run from the repository root:
//
//	cd scripts && go test antislop.go antislop_test.go

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustData(t *testing.T) *data {
	t.Helper()
	db, err := loadData()
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	return db
}

func hasFinding(fs []Finding, rule, match string) bool {
	for _, f := range fs {
		if f.Rule == rule && f.Match == match {
			return true
		}
	}
	return false
}

func describe(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		k := f.Rule
		if f.Kind != "" && f.Rule == "fidelity" {
			k += "/" + f.Kind
		}
		out = append(out, k+":"+f.Match)
	}
	return out
}

func hardOnly(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Hard {
			out = append(out, f)
		}
	}
	return out
}

// ------------------------------------------------------------ data

func TestDataLoads(t *testing.T) {
	db := mustData(t)
	if len(db.glossary) < 80 {
		t.Errorf("glossary has %d rows, want ≥ 80", len(db.glossary))
	}
	t.Logf("banned %d, allowed %d, domain %d, glossary %d, hedges %d",
		len(db.banned), len(db.allowed), len(db.domain), len(db.glossary), len(db.hedges))
}

// TestGlossaryNeverSafe: a row's preferred and also-OK words never match its own never patterns.
func TestGlossaryNeverSafe(t *testing.T) {
	db := mustData(t)
	for _, r := range db.glossary {
		src := newDoc("We see " + strings.TrimSuffix(ticked(r.English)[0], "*") + " here.")
		if !enHas(src, r.en) {
			t.Errorf("%s: its first English pattern does not match itself", r.English)
		}
		out := newDoc(r.Preferred + ". " + r.AlsoOK + ".")
		for _, f := range glossaryCheck(db, src, out) {
			t.Errorf("%s: preferred/also-OK word %q fires never %s", r.English, f.Match, r.Never)
		}
	}
}

// TestLexiconExamples: the bad side of each ru-non-approved example fires its row, the good side does not.
func TestLexiconExamples(t *testing.T) {
	db := mustData(t)
	for _, row := range db.banned {
		t.Run(row.Term, func(t *testing.T) {
			bad, good, ok := strings.Cut(row.Example, "→")
			if !ok {
				t.Fatalf("example %q has no →", row.Example)
			}
			fired := func(s string) bool {
				s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "«"), "»")
				for _, f := range check(db, newDoc(s)) {
					if f.Term == row.Term {
						return true
					}
				}
				return false
			}
			if !fired(bad) {
				t.Errorf("bad side %q does not fire", bad)
			}
			if fired(good) {
				t.Errorf("good side %q fires", good)
			}
		})
	}
}

// ------------------------------------------------------------ Russian text rules

func TestInflection(t *testing.T) {
	db := mustData(t)
	rowFor := func(w string) string {
		for _, r := range db.banned {
			if matchesOne(r.pats, norm(w)) {
				return r.Term
			}
		}
		return ""
	}
	tests := []struct {
		word string
		want bool
	}{
		{"оракул", true}, {"оракулом", true}, {"Оракулов", true},
		{"скипнуть", true}, {"скипнутым", true}, {"заскипан", true}, {"скипает", true},
		{"гейта", true}, {"дельту", true}, {"ногу", true}, {"узлы", true}, {"ревьюерами", true},
		{"мержем", true}, {"смержить", true}, {"демотировать", true}, {"ретраит", true},
		{"покраснеет", true}, {"глотает", true}, {"молчит", true}, {"громкой", true}, {"дефолтный", true},
		{"флапает", true}, {"флаппинга", true}, {"пофиксить", true}, {"жжёт", true},
		{"фиксирует", false}, {"зафиксировать", false}, {"фикстура", false}, {"локальный", false},
		{"пинг", false}, {"молча", false}, {"громко", false}, {"красным", false}, {"ногти", false},
		{"ревью", false}, {"гейтвей", false}, {"узелок", false}, {"дельтаплан", false}, {"тредмил", false},
	}
	for _, tc := range tests {
		if got := rowFor(tc.word) != ""; got != tc.want {
			t.Errorf("%q: match=%v want %v (row %q)", tc.word, got, tc.want, rowFor(tc.word))
		}
	}
}

func TestCheckRules(t *testing.T) {
	db := mustData(t)
	tests := []struct {
		name           string
		text           string
		want, wantNone []string // rule:match
	}{
		{"word boundary", "Проверка «гейта» прошла, подгейтовый слой не трогаем.", []string{"lexicon:гейта"}, []string{"lexicon:подгейтовый"}},
		{"apostrophe glue", "Это ломает guard'ом всю цепочку.", []string{"glued:guard'ом"}, nil},
		{"hyphen glue", "Закрыт prior-тред, но error-строка осталась в CI-матрице.",
			[]string{"glued:prior-тред", "glued:error-строка", "glued:CI-матрице", "lexicon:prior", "lexicon:тред"}, nil},
		{"listed compound", "HTTP-запросы и gRPC-клиента не трогаем.", nil, []string{"glued:HTTP-запросы", "glued:gRPC-клиента"}},
		{"code spans skipped", "Флаг `скипнуть гейт` и ```дельта``` не проверяются.", nil, []string{"lexicon:скипнуть", "lexicon:гейт", "lexicon:дельта"}},
		{"fenced block skipped", "Пример:\n```\nскипнуть гейт prior\n```\nКонец.", nil, []string{"lexicon:скипнуть", "lexicon:prior"}},
		{"latin prose soft", "Срабатывает fallback, CI и API в порядке.", []string{"latin-prose:fallback"}, []string{"latin-prose:CI", "latin-prose:API"}},
		{"phrase with number", "Его прислали с уверенностью 5/10.", []string{"lexicon:уверенностью 5"}, nil},
		{"noga CI slang", "Ночная матрица запускает ногу `admin`.", []string{"lexicon:ногу"}, nil},
		{"noga accounting", "Сначала соберите все ноги проводки, затем вызовите `Charge`.", []string{"domain:ноги"}, []string{"lexicon:ноги"}},
		{"uzel host", "Узел кластера недоступен.", []string{"domain:Узел"}, []string{"lexicon:Узел"}},
		{"uzel test", "Каждый такой узел становится `skipped`.", []string{"lexicon:узел"}, nil},
		{"url skipped", "Смотри https://example.com/skip/prior здесь.", nil, []string{"lexicon:prior", "latin-prose:skip"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := check(db, newDoc(tc.text))
			for _, w := range tc.want {
				rule, match, _ := strings.Cut(w, ":")
				if !hasFinding(fs, rule, match) {
					t.Errorf("missing %s; got %v", w, describe(fs))
				}
			}
			for _, w := range tc.wantNone {
				rule, match, _ := strings.Cut(w, ":")
				if hasFinding(fs, rule, match) {
					t.Errorf("unexpected %s; got %v", w, describe(fs))
				}
			}
		})
	}
}

func TestLengthAndFacts(t *testing.T) {
	db := mustData(t)
	tests := []struct {
		name               string
		text               string
		wantLen, wantFacts bool
	}{
		{"25 words ok", strings.TrimSpace(strings.Repeat("слово ", 25)) + ".", false, false},
		{"26 with code span", strings.Repeat("слово ", 25) + "`a b c`.", true, false},
		{"two sentences", strings.Repeat("слово ", 14) + "конец. Новое " + strings.Repeat("слово ", 13) + "конец.", false, false},
		{"three commas", "Запрос падает, лог пишет ошибку, метрика растёт, алерт молчит.", false, true},
		{"а after comma counts once", "Тест проходит, а проверка не выполняется.", false, false},
		{"commas in code ignored", "Вызов `f(a, b, c, d)` падает.", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotLen, gotFacts := false, false
			for _, f := range check(db, newDoc(tc.text)) {
				gotLen = gotLen || f.Rule == "length"
				gotFacts = gotFacts || f.Rule == "facts"
			}
			if gotLen != tc.wantLen || gotFacts != tc.wantFacts {
				t.Errorf("length=%v facts=%v, want %v %v", gotLen, gotFacts, tc.wantLen, tc.wantFacts)
			}
		})
	}
}

// ------------------------------------------------------------ fixtures: synthetic slop cases and clean text

// expectedSlop is the hand label for each case in testdata/slop_cases.json. The cases are synthetic
// review comments on invented code. Each item is a surface form; every whole-word occurrence must get
// a hard finding. Accounting «ноги» in S19 and S20 are domain words (soft), so they are not labelled.
var expectedSlop = map[string][]string{
	"S01": {"заскипан", "CI-матрице", "Гейт", "ногу", "пина", "скипнутым"},
	"S02": {"Гард", "узел", "жечь", "узлы", "треде"},
	"S03": {"флапает", "ретрая", "глотает", "ассерт"},
	"S04": {"щит", "оракул", "герметичная", "мутантов"},
	"S05": {"дельту", "prior-замечания", "ревьюер", "уверенностью 7", "Что отброшено", "codex"},
	"S06": {"демотировать", "error-строка", "шторм", "громких"},
	"S07": {"скипнуть", "лейн", "лейны", "модалку", "дисмисс", "кебаб"},
	"S08": {"сьют", "скипается", "CI-ногу", "Хендлер", "мержем"},
	"S09": {"флаппинга", "фолбэк", "молчать", "дыры"},
	"S10": {"Резолв", "лок", "дедуп", "батч"},
	"S11": {"Smoke-узлы", "узел", "краснеет", "оракул"},
	"S12": {"ручка", "флоу", "дефолтный", "хелпера"},
	"S13": {"скоупа", "fallback-клик", "Кейсы", "копирайта"},
	"S14": {"ридера", "SQLite-лог", "демоушена", "reader-диагностика"},
	"S15": {"TestRail-репортёр", "skipped-результаты", "кейсы", "CI-сервисов"},
	"S16": {"сторожа", "схлопнулась", "различающую силу", "линия защиты"},
	"S17": {"Фикс", "грейсе", "пофиксить", "Error-строка"},
	"S18": {"in-diff", "resolved_prior", "замечание отклонено", "не публикую"},
	"S19": {"батч"},
	"S20": {"Outbox-события", "дедупа"},
}

// cleanText are sentences in the target style. They must produce no finding at all.
var cleanText = []string{
	"Набор `checkout-e2e` пропущен в ночной матрице CI.",
	"В ночной матрице CI есть только общий запуск `APP_ENV=prod`.",
	"Поэтому на стандартном предрелизном контуре набор пропущен целиком (`skipped`).",
	"Если ответ API приходит позже таймаута, запрос повторяется один раз.",
	"Тест `test_orders_list_matches_db` проходит, даже если шлюз всегда отдаёт пустой список.",
	"После ревью в MR появился новый коммит.",
	"Кэш не сбрасывается после релиза.",
	"Пайплайн падает на шаге `lint`.",
	"```go\nfunc skipGate() { retry(\"дельта\", \"нога\") } // скипнуть гейт prior-тред\n```",
	"Метод `deleteAllTags` ищет меню по всему документу.",
	"Файл `internal/ledger/transfer.go:64` отправляет весь пакет одним вызовом `PostTransfer`.",
	"Повтор упрётся в ошибку дубликата `entry_id`.",
	"Проверил изменения после прошлого ревью и замечания из него.",
	"HTTP-запрос к сервису возвращает код 500 через 30 секунд.",
	"gRPC-клиент не закрывает соединение после таймаута.",
	"Строка уходит только в лог уровня `Debug`.",
	"Зафиксируйте версию в `go.mod`. Проверка фиксирует время запуска в фикстуре.",
	"Локальный кэш хранит результат пинга до хоста. Код отбрасывает дробную часть.",
	"**Предложение:** добавьте отдельный запуск `APP_ENV=staging`.",
	"Тест-кейс в TestRail остаётся без нового статуса.",
	"Линтер `golangci-lint@v1.60.1` не работает с `go 1.26`.",
	"Код молча проводит отрицательную сумму как положительную.",
	"См. `internal/orders/store.go:26`: метод пишет весь результат через `log.Info`.",
}

type fixtureCase struct {
	Case string `json:"case"`
	Noga string `json:"noga"`
	Text string `json:"text"`
}

func loadFixtures(t *testing.T) []fixtureCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "slop_cases.json"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var cs []fixtureCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(cs) != len(expectedSlop) {
		t.Fatalf("want %d cases, got %d", len(expectedSlop), len(cs))
	}
	return cs
}

type span struct{ a, b int }

// occurrences returns rune ranges of whole-word occurrences of item in r (a hyphen or apostrophe glues words).
func occurrences(r []rune, item string) []span {
	it := []rune(item)
	var out []span
	for i := 0; i+len(it) <= len(r); i++ {
		if string(r[i:i+len(it)]) != item {
			continue
		}
		j := i + len(it)
		if i > 0 && (isWordRune(r[i-1]) || isJoiner(r[i-1])) {
			continue
		}
		if j < len(r) && (isWordRune(r[j]) || (isJoiner(r[j]) && j+1 < len(r) && isWordRune(r[j+1]))) {
			continue
		}
		out = append(out, span{i, j})
	}
	return out
}

// TestSlopCases: every case fires; recall per labelled occurrence and precision per hard finding ≥ 0.95.
func TestSlopCases(t *testing.T) {
	db := mustData(t)
	var expected, found, tp, fp int
	for _, c := range loadFixtures(t) {
		t.Run(c.Case, func(t *testing.T) {
			items, ok := expectedSlop[c.Case]
			if !ok {
				t.Fatalf("no labels for %s", c.Case)
			}
			d := newDoc(c.Text)
			hard := hardOnly(check(db, d))
			if len(hard) == 0 {
				t.Errorf("%s: no hard finding", c.Case)
			}
			var exp []span
			for _, it := range items {
				occ := occurrences(d.r, it)
				if len(occ) == 0 {
					t.Fatalf("label %q not in text", it)
				}
				exp = append(exp, occ...)
			}
			overlap := func(e span, f Finding) bool { return e.a < f.end && f.start < e.b }
			var missed, extra []string
			for _, e := range exp {
				hit := false
				for _, f := range hard {
					hit = hit || overlap(e, f)
				}
				if hit {
					found++
				} else {
					missed = append(missed, string(d.r[e.a:e.b]))
				}
			}
			expected += len(exp)
			for _, f := range hard {
				hit := false
				for _, e := range exp {
					hit = hit || overlap(e, f)
				}
				if hit {
					tp++
				} else {
					fp++
					extra = append(extra, f.Rule+":"+f.Match)
				}
			}
			t.Logf("%s hard %d missed=%v extra=%v", c.Case, len(hard), missed, extra)
		})
	}
	recall := float64(found) / float64(expected)
	precision := float64(tp) / float64(tp+fp)
	t.Logf("slop cases: recall %d/%d = %.3f, precision %d/%d = %.3f", found, expected, recall, tp, tp+fp, precision)
	if recall < 0.95 || precision < 0.95 {
		t.Errorf("recall %.3f or precision %.3f below 0.95", recall, precision)
	}
}

// TestDomainLegsSoft: accounting «ноги» in the domain cases are soft domain notes, never hard;
// CI «ноги» in the slang cases are hard lexicon findings.
func TestDomainLegsSoft(t *testing.T) {
	db := mustData(t)
	seen := map[string]int{}
	for _, c := range loadFixtures(t) {
		if c.Noga == "" {
			continue
		}
		seen[c.Noga]++
		var hard, soft int
		for _, f := range check(db, newDoc(c.Text)) {
			if !strings.HasPrefix(norm(f.Match), "ног") && !strings.Contains(norm(f.Match), "-ног") {
				continue
			}
			switch {
			case f.Rule == "lexicon":
				hard++
			case f.Rule == "domain":
				soft++
			}
		}
		switch c.Noga {
		case "domain":
			if hard > 0 || soft == 0 {
				t.Errorf("%s: domain legs gave %d hard lexicon and %d soft domain findings, want 0 and ≥ 1", c.Case, hard, soft)
			}
		case "slang":
			if hard == 0 || soft > 0 {
				t.Errorf("%s: CI legs gave %d hard lexicon and %d soft domain findings, want ≥ 1 and 0", c.Case, hard, soft)
			}
		}
	}
	if seen["domain"] < 2 || seen["slang"] < 2 {
		t.Errorf("want ≥ 2 domain and ≥ 2 slang cases, got %v", seen)
	}
}

func TestCleanText(t *testing.T) {
	db := mustData(t)
	for i, s := range cleanText {
		for _, f := range check(db, newDoc(s)) {
			t.Errorf("clean_%02d: false positive %s %q (%s) in %q", i, f.Rule, f.Match, f.Hint, s)
		}
	}
}

// ------------------------------------------------------------ fidelity, Russian source (ported)

func TestFidelityRU(t *testing.T) {
	db := mustData(t)
	src := "Метод `foo()` в `internal/a.go:12` падает через 30 секунд. Запрос идёт на POST /orders/checkout. " +
		"Вероятно, это может повториться. Скорее всего, около 5 раз. Индекс порядка не важен."
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"all kept", "Метод `foo()` в `internal/a.go:12` падает через 30 секунд. Запрос POST /orders/checkout. " +
			"Вероятно, это может повториться. Скорее всего, около 5 раз.", nil},
		{"span changed", "Метод `foo` в `internal/a.go:12` падает через 30 секунд. POST /orders/checkout. " +
			"Вероятно, может повториться. Скорее всего, около 5 раз.", []string{"code:`foo()`"}},
		{"number lost", "Метод `foo()` в `internal/a.go:12` падает. POST /orders/checkout. " +
			"Вероятно, может повториться. Скорее всего, около 5 раз.", []string{"number:30"}},
		{"path lost", "Метод `foo()` в `internal/a.go:12` падает через 30 секунд. " +
			"Вероятно, может повториться. Скорее всего, около 5 раз.", []string{"path:/orders/checkout"}},
		{"hedges lost", "Метод `foo()` в `internal/a.go:12` падает через 30 секунд. POST /orders/checkout. " +
			"Это повторится. Пять раз: 5.", []string{"hedge:может", "hedge:Вероятно", "hedge:около 5"}},
		{"same strength, other word", "Метод `foo()` в `internal/a.go:12` падает через 30 секунд. POST /orders/checkout. " +
			"Вероятнее всего, это могут повторить. По-видимому, примерно 5 раз.", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range fidelity(db, newDoc(src), newDoc(tc.out), false) {
				got = append(got, f.Kind+":"+f.Match)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// ------------------------------------------------------------ cross-language: English source → Russian text

// TestENtoRU: short English review sentences and Russian renderings. want lists rule[/kind]:match that
// must be present; good pairs (want == nil) must have no hard finding at all.
func TestENtoRU(t *testing.T) {
	db := mustData(t)
	tests := []struct {
		name, en, ru string
		want         []string
	}{
		{"good: flaky with a number",
			"The test `TestPay` is flaky: it fails about 1 time in 5 runs.",
			"Тест `TestPay` нестабилен: он падает примерно 1 раз из 5 прогонов.", nil},
		{"good: advice kept, decimal comma",
			"You should pin the image tag in `Dockerfile`. The timeout is 2.5 seconds.",
			"Стоит закрепить тег образа в `Dockerfile`. Таймаут — 2,5 секунды.", nil},
		{"good: skipped status in backticks",
			"When `APP_ENV` is not `staging`, the suite is skipped.",
			"Если `APP_ENV` не равен `staging`, набор тестов пропущен (`skipped`).", nil},
		{"slop: flaky rendered as флаки",
			"The test `TestPay` is flaky: it fails about 1 time in 5 runs.",
			"Тест `TestPay` флаки: он падает примерно 1 раз из 5 прогонов.",
			[]string{"glossary:флаки"}},
		{"slop: skip rendered as скипается",
			"When `APP_ENV` is not `staging`, the suite is skipped.",
			"Если `APP_ENV` не равен `staging`, сьют скипается.",
			[]string{"glossary:сьют", "glossary:скипается"}},
		{"slop: over-russified CI",
			"The CI pipeline fails on the `lint` step.",
			"Сборочный конвейер падает на шаге `lint`.",
			[]string{"glossary:Сборочный конвейер"}},
		{"slop: pipeline jargon and glue",
			"Two prior findings are still open; the reviewer found 3 new ones.",
			"Два prior-замечания всё ещё открыты; ревьюер нашёл 3 новых.",
			[]string{"glued:prior-замечания", "glossary:ревьюер"}},
		{"dropped hedge: may",
			"This may lose the event after a retry.",
			"После повторной попытки событие теряется.",
			[]string{"fidelity/hedge:may"}},
		{"dropped hedge: likely",
			"The cache is likely stale after 30 seconds.",
			"Через 30 секунд кэш устаревает.",
			[]string{"fidelity/hedge:likely"}},
		{"changed strength: might → вероятно",
			"The handler might return 500.",
			"Обработчик, вероятно, вернёт 500.",
			[]string{"fidelity/hedge:might"}},
		{"changed identifier",
			"Call `deleteAllTags()` in `admin/tags.py:41`.",
			"Вызовите `DeleteAllTags()` в `admin/tags.py:41`.",
			[]string{"fidelity/code:`deleteAllTags()`"}},
		{"invented certainty",
			"The worker can drop messages when the queue is full.",
			"Если очередь заполнена, воркер всегда теряет сообщения.",
			[]string{"fidelity/certainty:всегда"}},
		{"dropped must",
			"You must rotate the token before the release.",
			"Перед релизом смените токен.",
			[]string{"fidelity/hedge:must"}},
		{"could not is a fact, not a hedge",
			"The client could not connect to `redis:6379`.",
			"Клиент не смог подключиться к `redis:6379`.", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, lang := analyze(db, tc.ru, tc.en, true)
			if lang != "en" {
				t.Fatalf("source language %q, want en", lang)
			}
			hard := hardOnly(fs)
			if tc.want == nil && len(hard) > 0 {
				t.Errorf("good pair has hard findings: %v", describe(hard))
			}
			got := describe(hard)
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("missing %s; got %v", w, got)
				}
			}
		})
	}
}

// ------------------------------------------------------------ CLI

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	clean := write("clean.md", "Набор пропущен в ночной матрице CI.")
	slop := write("slop.md", "Набор заскипан в CI-матрице.")
	soft := write("soft.md", "Срабатывает fallback.")
	ruSrc := write("src.md", "Набор `x` заскипан через 30 секунд.")
	enSrc := write("en.md", "The suite `x` may be skipped after 30 seconds.")
	enGood := write("en-good.md", "Набор `x` может быть пропущен через 30 секунд.")
	enBad := write("en-bad.md", "Набор `x` скипается через 30 секунд.")
	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		wantOut  string
	}{
		{"clean", []string{clean}, "", 0, "0 hard, 0 soft"},
		{"slop", []string{slop}, "", 1, "HARD lexicon"},
		{"soft only exits 0", []string{soft}, "", 0, "0 hard, 1 soft"},
		{"stdin", []string{"-"}, "Проверил текущую дельту.", 1, "дельту"},
		{"json", []string{"--json", slop}, "", 1, `"rule": "glued"`},
		{"ru source", []string{"--source", ruSrc, clean}, "", 1, "src 1:"},
		{"en source good", []string{"--source", enSrc, enGood}, "", 0, "0 hard, 0 soft (source en)"},
		{"en source slop", []string{"--source", enSrc, enBad}, "", 1, "HARD glossary"},
		{"en source hedge", []string{"--source", enSrc, enBad}, "", 1, "fidelity"},
		{"lookup", []string{"--lookup", "flaky"}, "", 0, "нестабильный тест"},
		{"lookup russian", []string{"--lookup", "скипнуть"}, "", 0, "[ru-non-approved]"},
		{"lookup hedge", []string{"--lookup", "likely"}, "", 0, "probable"},
		{"lookup json", []string{"--json", "--lookup", "гейт"}, "", 0, `"table": "glossary-en-ru"`},
		{"lookup miss", []string{"--lookup", "zzzzqq"}, "", 1, "no entry"},
		{"no file", nil, "", 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tc.args, strings.NewReader(tc.stdin), &out, &errb)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d; out=%s err=%s", code, tc.wantCode, out.String(), errb.String())
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantOut, out.String())
			}
		})
	}
}

// TestSummary logs the numbers quoted in reports.
func TestSummary(t *testing.T) {
	db := mustData(t)
	kinds := map[string]int{}
	for _, r := range db.banned {
		kinds[r.Kind]++
	}
	t.Logf("banned rows by kind: %v; glossary %d; hedges %d", kinds, len(db.glossary), len(db.hedges))
}
