# antislop

## TL;DR (for humans)

`antislop` contains two skills for clear technical text:

- `antislop-eng` writes English in controlled technical English. Its checker examines the text against an approved-word dictionary and a set of writing rules.
- `antislop-rus` translates English text into clear Russian, or cleans a Russian review comment. Its gate finds slang, Cyrillized English and missing hedges, numbers or identifiers.

For a review comment in Russian, use the two skills in sequence. First, write the English text with `antislop-eng`. Then translate this text with `antislop-rus`.

Go 1.22 or higher is necessary. The sections below give the instructions for AI agents.

### Кратко

`antislop` содержит два навыка для ясного технического текста:

- `antislop-eng` пишет английский текст простым техническим языком. Его проверка сравнивает текст со словарём одобренных слов и набором правил.
- `antislop-rus` переводит английский текст на ясный русский язык или чистит русское замечание к коду. Его проверка находит сленг и английские слова, записанные кириллицей. Ещё она находит потерянные оговорки, числа или идентификаторы.

Чтобы написать замечание к коду на русском языке, используйте два навыка по очереди. Сначала напишите английский текст с помощью `antislop-eng`. Затем переведите этот текст с помощью `antislop-rus`.

Нужен Go 1.22 или более новая версия. Инструкции для ИИ-агентов приведены в разделах ниже.

### Before and after

English, before:

```text
The new leg in the nightly matrix is the oracle for this check, but it is not hermetic.
```

English, after:

```text
The new nightly CI job gives the correct result for this check. But this job is not isolated from other systems.
```

Russian, before:

```text
Проверил текущую дельту: два prior-замечания закрыты, ревьюер подтвердил это с уверенностью 7.
```

Russian, after:

```text
Проверил изменения после прошлого ревью. Два прежних замечания исправлены.
```

## What the repository contains

This repository contains two Agent Skills for clear technical text. Each skill has one Go gate. A gate gives the same result each time that it examines the same text. The skills change only the wording of a text. They keep all facts, identifiers, numbers, conditions and hedge strengths.

- **antislop-eng** writes and examines English against an approved-word dictionary and a set of writing rules. It contains the rules in `SKILL.md`, the dictionary `scripts/dictionary.json` and its gate, the checker `check.go`.
- **antislop-rus** translates English engineering text into clear Russian. It also cleans a review comment that is in Russian. It contains an EN→RU glossary, a table of hedge strengths, style rules and the gate `antislop.go`.

Each skill directory contains these items:

- `SKILL.md`: the procedure for an AI agent
- `scripts/`: the gate and its data. The gate is one Go file. The file uses only the standard library and embeds its data.

You can also use the gates without an agent.

## Requirements

Go 1.22 or higher is necessary. No other item is necessary. The skills have no `go.mod` file and no dependencies.

1. Do a check of the Go version:

```sh
go version
```

2. If the output shows `go1.22` or a higher version, go to "Install".
3. If Go is not installed, or if the version is lower than 1.22, install Go. Use the procedure for your operating system.

**macOS:**

```sh
brew install go
```

**Debian or Ubuntu:**

```sh
sudo apt-get install -y golang-go
```

The Debian or Ubuntu package can contain a version that is lower than 1.22. If `go version` shows a version lower than 1.22, install the official tarball.

**Linux, official tarball:**

1. Download the Linux tarball from https://go.dev/dl/.
2. Install the tarball in `/usr/local/go`. The command removes `/usr/local/go` before it installs the new version:

```sh
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go<version>.linux-amd64.tar.gz
```

3. Add the Go directory to `PATH`:

```sh
export PATH=$PATH:/usr/local/go/bin
```

**Windows:**

```powershell
winget install GoLang.Go
```

4. After the installation, use `go version` again. The output must show `go1.22` or a higher version.

## Install

To install the skills for Claude Code, copy each skill directory into `~/.claude/skills/`. Use these commands in the root directory of this repository:

```sh
cp -R skills/antislop-eng ~/.claude/skills/antislop-eng
cp -R skills/antislop-rus ~/.claude/skills/antislop-rus
```

Other agents that read Agent Skills can use the same directories. The commands in each `SKILL.md` use the paths `~/.claude/skills/antislop-eng` and `~/.claude/skills/antislop-rus`.

An installed skill has no `go.mod` file, and a `go.mod` file is not necessary. You can use `go run` on the gate file from all directories, because the gate embeds its data with `go:embed`.

To make sure that the installation is correct, do one lookup for each skill:

```sh
go run ~/.claude/skills/antislop-eng/scripts/check.go --lookup may
go run ~/.claude/skills/antislop-rus/scripts/antislop.go --lookup flaky
```

The installation is correct if these conditions occur:

- The two commands stop with exit code 0.
- The antislop-eng output starts with `== may ==`.
- The antislop-rus output starts with `flaky →`.

## Select the skill and the mode

Use these rules to select the skill:

- If you write or examine English technical text, use antislop-eng.
- If you translate English technical text into Russian, use antislop-rus.
- If you clean a review comment that is in Russian, use antislop-rus.
- If you write a code-review comment, an incident note or a status report in Russian, use the pipeline.

The checker uses a controlled vocabulary and these rules:

- One approved word for each meaning
- One part of speech for each word
- One instruction in each sentence
- Active voice
- Simple tenses
- Hard length limits: 20 words for a procedural sentence and 25 words for a descriptive sentence.

The rules are applicable to manuals and procedures. The skill also uses them for these texts:

- Code-review comments
- Comments on merge requests
- Incident reports
- Tool descriptions
- Error messages.

For antislop-eng, select the mode from the text type:

- For procedures, instructions, safety text, error messages, tool descriptions and task plans for agents, use **Strict** mode. In Strict mode, all rules are applicable, and the dictionary is also applicable.
- For explanations, code-review comments, design notes and status reports, use **Relaxed** mode. In Relaxed mode, all structure rules are applicable.

In Relaxed mode, examine each non-approved word that the checker reports. If the approved word keeps the meaning, use the approved word. If the approved word changes the meaning, keep the word of the source.

In the two modes, keep the strength of each hedge:

- Write *may* as POSSIBLY or IT IS POSSIBLE THAT.
- Write *likely* as IT IS VERY POSSIBLE THAT.
- If *should* is advice, write WE RECOMMEND THAT.
- If *should* is an obligation, write MUST.

## The pipeline

The two skills are the two steps of one pipeline. Use the pipeline for code-review comments, incident notes and status reports:

1. **Use antislop-eng to write the English text.** The result is short English text with one meaning for each sentence. All hedges keep their strength.
2. **Use antislop-rus to translate the English text.** The result is Russian text with no slang and no Cyrillized English. The Russian text keeps all hedges, numbers and identifiers.

For each step, do the same gate loop:

1. Use the gate on the draft.
2. Correct only the items that the gate shows. Do not change one byte of a sentence that has no finding.
3. Use the gate again.
4. Stop after 3 cycles of the loop. If you keep a finding on purpose, write the finding on one line after the text.

You can also use each skill without the other skill. Use antislop-eng on English technical text. Use antislop-rus on an English source text or on a Russian review comment.

## Use the gates

### check.go (antislop-eng)

```sh
# Check a document. The procedural or descriptive mode is detected per sentence.
go run skills/antislop-eng/scripts/check.go draft.md

# Add project terms to the built-in software glossary
go run skills/antislop-eng/scripts/check.go draft.md --glossary terms.txt

# Force a mode and print JSON; flags can come before or after the file
go run skills/antislop-eng/scripts/check.go draft.md --mode procedural --json

# Read from stdin
cat draft.md | go run skills/antislop-eng/scripts/check.go -

# Look up words in the dictionary
go run skills/antislop-eng/scripts/check.go --lookup check ensure "turn on" may
```

Flags:

| flag | meaning |
|---|---|
| `--mode procedural\|descriptive\|auto` | sentence limit of 20 (procedural) or 25 (descriptive) words; `auto` (default) detects it per sentence |
| `--glossary FILE` | extra technical nouns and verbs, one per line, multi-word allowed; added to the built-in list |
| `--no-default-glossary` | do not load the built-in software glossary (`scripts/glossary/software.txt`) |
| `--json` | machine-readable report |
| `--lookup WORD ...` | print the dictionary entry, the approved alternatives and a hedge hint for each word |

Select `--mode` from the text:

- If the text tells the reader to do something, set `--mode procedural`.
- If the text gives information, set `--mode descriptive`.
- If the text contains the two types, use the default `auto` mode.

The checker reports these items:

- Non-approved words, with their alternatives
- Words that are not in the dictionary
- Sentences that are longer than the limit (the checker counts words as Rules 8.4 to 8.7 tell)
- Perfect tenses
- Passive voice
- -ing verb forms
- Semicolons
- Contractions
- Latin abbreviations
- Gendered pronouns
- Paragraphs of more than 6 sentences
- Noun clusters of 4 or more words.

The checker does not examine code blocks and inline code spans. It counts a code span as one word.

The checker accepts these items as technical nouns and shows them in the report:

- Code tokens: paths, `file.go:12`, `snake_case`, `camelCase`, dotted names, flags and URLs
- Short acronyms in capital letters.

Exit codes:

| code | meaning |
|---|---|
| 0 | report printed, with or without findings; `--lookup` done; `-h` |
| 1 | I/O error (file not found), or no file given |
| 2 | usage error (bad flag or mode) |

The exit code does not show findings. Read the report.

### antislop.go (antislop-rus)

```sh
# Check a Russian text alone (use - for stdin)
go run skills/antislop-rus/scripts/antislop.go ru.md

# Check a translation against its English source (or a cleaned text against the Russian original)
go run skills/antislop-rus/scripts/antislop.go --source en.md ru.md

# Machine-readable output
go run skills/antislop-rus/scripts/antislop.go --json --source en.md ru.md

# Look up a word in the glossary, the hedge table and the lexicon (English or Russian)
go run skills/antislop-rus/scripts/antislop.go --lookup flaky скипнуть
```

Exit codes:

| code | meaning |
|---|---|
| 0 | no hard findings (soft findings may exist); with `--lookup`, at least one entry found |
| 1 | at least one hard finding; with `--lookup`, no entry found |
| 2 | usage or I/O error |

The gate reports findings for these rules:

| rule | severity | meaning |
|---|---|---|
| `lexicon` | hard | a banned Russian word, in any inflection |
| `glued` | hard | Latin and Cyrillic in one token, unless it is a listed compound |
| `glossary` | hard | with an English source: the text uses a «never» rendering of a source term |
| `fidelity` | hard | with a source: a code span, `file:line`, path, number or hedge is missing, or the text adds certainty |
| `domain` | soft | a domain word («ноги», «узел») in its literal sense |
| `latin-prose` | soft | a bare English word outside backticks |
| `length` | soft | a sentence over 25 words |
| `facts` | soft | a sentence with 3 or more clause breaks |

Russian review comments from developers and from language models frequently contain words of these types:

- Slang: «нога» for a CI job, «флапает»
- Cyrillized English: «скипнуть», «гейт», «ретрай»
- Review-pipeline jargon: «дельта», «prior-замечания», «ревьюер»
- Latin glued to Cyrillic: «guard'ом», «CI-матрица».

Before the reader can know the fact, the reader must find the meaning of each of these words. Translations from English add one more problem. The translations do not keep hedges («may lose» becomes «теряет»), and they change identifiers.

antislop-rus does not accept these words. antislop-rus keeps these words and items:

- Standard loanwords: CI, ревью, коммит, релиз, таймаут, кэш, тест, API, пайплайн, MR
- Standard compounds: «HTTP-запрос», «gRPC-клиент»
- Domain words in their literal sense: «ноги» of a posting, «узел» of a cluster
- All identifiers, byte for byte.

Before you translate, record each identifier, number, condition, hedge, alternative and example of the source in a fact ledger.

## How to use the skills well

### Select the mode and the glossary

1. Select the mode from the document type:
   - For design notes, explanations and review comments, use Relaxed mode.
   - For procedures, task plans for agents and instructions, use Strict mode.
2. Keep one glossary file for each feature or document set. Use the same file for all related documents.
3. Before you add a term to the glossary, use `--lookup` on the term.
4. Add the term only if the two conditions that follow occur:
   - The term fits a technical-noun or technical-verb category (Rule 1.5 or 1.12).
   - `--lookup` shows no approved word with the same meaning.
5. Do not add the terms of the built-in software glossary. By default, the checker uses that glossary.

### Use the checker

1. Use the checker with the glossary: `go run ~/.claude/skills/antislop-eng/scripts/check.go <file> --glossary <glossary file>`.
2. For descriptive text, add `--mode descriptive`. For procedures, add `--mode procedural`.
3. Read the full report. The exit code is 0 also when the report has findings.
4. Correct each hard finding. Make a decision about each finding with the mark `?`.
5. Use the checker again. Stop when you can give a cause for each finding that you keep.

Some findings are usually correct to keep:

- A possessive "has" that the checker reports as a perfect tense (Rule 3.4)
- Noun clusters in tables or in lists of names.

### Do the walk

The gate loop is not the full pass. The full pass is the gate loop and the walk. After the gate loop, do the walk. In the walk, read the text for these items, because no script finds them:

- One topic in each sentence
- The condition first, then the command
- The approved meaning of each word
- No ambiguous *with*, *this* or pronoun
- No dash that connects two topics in one sentence
- No missing article, subject or verb
- A list for 3 or more steps or conditions
- One name for one item in the full document
- The hedge strength of the source, as the hedge table of the skill shows.

If a document has more than approximately 1500 words, do the walk for each section. Then give the full document to an adversarial verifier. The verifier must reject a change if the change removes or changes a number, condition, hedge, identifier or code span.

Do not change code, commands, identifiers, paths, numbers or the text of error messages.

### Write a review text in Russian

1. Use antislop-eng to write the English text.
2. Do the gate loop and the walk on the English text.
3. Use antislop-rus to translate the English text into Russian.
4. Use the antislop-rus gate on the translation. Set `--source` to the English file.
5. Correct only the items that the gate shows. Do a maximum of 3 cycles.
6. After each change to a text, do the full pass on that text again.

## Examples

No example comes from a code review.

### antislop-eng examples

Each report is an accurate copy of the output of `go run skills/antislop-eng/scripts/check.go <file> --glossary terms.txt`. By default, the checker uses the built-in software glossary. The glossary file `terms.txt` adds five terms:

```text
caller
event
message
nightly
payment
```

#### Example 1: slang

Before:

```text
The new leg in the nightly matrix is the oracle for this check, but it is not hermetic.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 0   Non-approved words: 0   Not approved as a verb: 0   Words not in dictionary: 4

WORDS NOT IN THE DICTIONARY — each must be a technical noun/verb (Rules 1.5, 1.12) or be replaced
------------------------------------------------------------
  leg x1, matrix x1, oracle x1, hermetic x1
  (Add legitimate technical terms to a glossary file and pass --glossary to silence them.)
```

After:

```text
The new nightly CI job gives the correct result for this check. But this job is not isolated from other systems.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 0   Non-approved words: 0   Not approved as a verb: 0   Words not in dictionary: 0

AUTO-GLOSSARY — code-like tokens accepted as technical nouns (Rules 1.5, 8.6); check the list
------------------------------------------------------------
  CI (acronym) x1

No findings. The text passes the mechanical checks. Still read it against the rules that no script can check (one topic per sentence, condition first, notes vs. instructions, approved meaning of each word).
```

#### Example 2: hedges

Before:

```text
This may lose the event after a retry. The cache is likely stale. You should add a test for `parseConfig`.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 0   Non-approved words: 1   Not approved as a verb: 1   Words not in dictionary: 3

NON-APPROVED WORDS (Rules 1.1–1.3, 9.1) — replace or restructure
------------------------------------------------------------
  should (v) x1  ->  MUST (v), IF (conj)

NOT APPROVED AS A VERB — fine if used as a noun / technical noun here; otherwise replace
------------------------------------------------------------
  lose x1  ->  as a verb use: DECREASE (v)

WORDS NOT IN THE DICTIONARY — each must be a technical noun/verb (Rules 1.5, 1.12) or be replaced
------------------------------------------------------------
  may x1, likely x1, stale x1
  (Add legitimate technical terms to a glossary file and pass --glossary to silence them.)

HEDGE WORDS — keep the strength of the claim; never change a possibility into a fact
------------------------------------------------------------
  should x1  ->  JUDGE: advice -> "WE RECOMMEND THAT ..."; obligation -> MUST. Do not change advice into an order by accident.
  may x1  ->  POSSIBLY, or "IT IS POSSIBLE THAT ..." (CAN only for ability or permission). Keep it a possibility.
  likely x1  ->  "IT IS VERY POSSIBLE THAT ...". Keep it a possibility, not a fact.
```

In the dictionary, *event* is not an approved general word. In a code review, an event is a technical noun. Thus, `terms.txt` contains the word *event*.

After:

```text
It is possible that the handler does not keep the event after a retry. It is very possible that the data in the cache is not the last version. We recommend that you add a test for `parseConfig`.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 0   Non-approved words: 0   Not approved as a verb: 0   Words not in dictionary: 0

No findings. The text passes the mechanical checks. Still read it against the rules that no script can check (one topic per sentence, condition first, notes vs. instructions, approved meaning of each word).
```

The three hedges keep their strength:

- *may* is IT IS POSSIBLE THAT.
- *likely* is IT IS VERY POSSIBLE THAT.
- *should* (advice) is WE RECOMMEND THAT.

#### Example 3: a 30-word sentence

Before:

```text
The handler in `internal/orders/retry.go:88` sends the error message to the caller when the upstream payment service sends an empty body and the retry counter is at the configured maximum value.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 1   Non-approved words: 0   Not approved as a verb: 0   Words not in dictionary: 2

WORDS NOT IN THE DICTIONARY — each must be a technical noun/verb (Rules 1.5, 1.12) or be replaced
------------------------------------------------------------
  body x1, configured x1
  (Add legitimate technical terms to a glossary file and pass --glossary to silence them.)

STRUCTURE AND GRAMMAR
------------------------------------------------------------
  [6.3] 30 words (descriptive, max 25): "The handler in `internal/orders/retry.go:88` sends the error message to the caller when..."   (para 1, sentence 1)
```

After:

```text
`internal/orders/retry.go:88` sends the error message to the caller when the two conditions that follow occur:

- The upstream payment service sends an empty response.
- The retry counter is at the maximum value in the configuration.
```

Checker output:

```text
DICTIONARY CHECK
============================================================
Structure/grammar findings: 0   Non-approved words: 0   Not approved as a verb: 0   Words not in dictionary: 0

No findings. The text passes the mechanical checks. Still read it against the rules that no script can check (one topic per sentence, condition first, notes vs. instructions, approved meaning of each word).
```

The list keeps the two conditions of the source. Two different sentences do not show that the two conditions must occur together.

### antislop-rus examples

Each gate output is an accurate copy of the output of `go run skills/antislop-rus/scripts/antislop.go <file>`.

#### Example 1: slang and Cyrillized English

Before:

```text
Ночная матрица запускает только общую ногу `APP_ENV=prod`, поэтому набор `checkout-e2e` остаётся скипнутым.
```

Gate output:

```text
HARD lexicon     1:39      «ногу»  slang: → запуск, job CI (учётные «ноги» — см. `ru-domain.md`) (в предложении есть признак сленга)
HARD lexicon     1:98      «скипнутым»  cyrillized: → пропуск, пропущен, пропустить
ex1.md: 2 hard, 0 soft
```

After:

```text
Ночная матрица CI содержит только общий запуск `APP_ENV=prod`. Поэтому набор `checkout-e2e` остаётся пропущенным.
```

Gate output: `ex1-after.md: 0 hard, 0 soft`.

#### Example 2: review-pipeline jargon

Before:

```text
Проверил текущую дельту: два prior-замечания закрыты, ревьюер подтвердил это с уверенностью 7.
```

Gate output:

```text
HARD lexicon     1:18      «дельту»  pipeline: → изменения с прошлого ревью
HARD lexicon     1:30      «prior»  pipeline: → прежний, из прошлого ревью
HARD glued       1:30      «prior-замечания»  латиница + русское слово: имя в backticks отдельно от русского слова, или русское слово целиком
HARD lexicon     1:55      «ревьюер»  pipeline: → убрать; писать сам вывод
HARD lexicon     1:80      «уверенностью 7»  pipeline: → убрать оценку уверенности
ex2.md: 5 hard, 0 soft
```

After:

```text
Проверил изменения после прошлого ревью. Два прежних замечания исправлены.
```

Gate output: `ex2-after.md: 0 hard, 0 soft`.

#### Example 3: test-theory metaphors and Latin glued to Cyrillic

Before:

```text
Новый щит в `billing-worker` проверяет только пустой список, и тест использует его как оракул. Ошибка пишется в error-строку внутри CI-матрицы.
```

Gate output:

```text
HARD lexicon     1:7       «щит»  metaphor: → защитная проверка
HARD lexicon     1:88      «оракул»  metaphor: → проверка, ожидаемый результат
HARD glued       1:113     «error-строку»  латиница + русское слово: имя в backticks отдельно от русского слова, или русское слово целиком
HARD glued       1:133     «CI-матрицы»  латиница + русское слово: имя в backticks отдельно от русского слова, или русское слово целиком
ex3.md: 4 hard, 0 soft
```

After:

```text
Новая защитная проверка в `billing-worker` сравнивает результат только с пустым списком. Ошибка пишется в строку уровня `Error` в матрице CI.
```

Gate output: `ex3-after.md: 0 hard, 0 soft`.

#### Example 4: a translation that loses a hedge and a number

English source:

```text
The handler in `internal/orders/retry.go:88` may lose the event after about 3 retries.
```

Before (a first translation):

```text
Хендлер в `internal/orders/retry.go:88` теряет событие после третьего ретрая.
```

Gate output with `--source`:

```text
HARD glossary    1:1       «Хендлер»  glossary «handler» → обработчик
HARD glossary    1:71      «ретрая»  glossary «retry» → повтор; повторная попытка (также: повторить)
HARD fidelity    src 1:77  «3»  число из исходника пропало
HARD fidelity    src 1:46  «may»  оговорка силы «possible»: в исходнике 1, в тексте 0 → возможно / может / могут / мог / могла / могло
HARD fidelity    src 1:71  «about 3»  оговорка силы «approx»: в исходнике 1, в тексте 0 → примерно / приблизительно / около N / порядка N
ex4.md: 5 hard, 0 soft (source en)
```

After:

```text
Обработчик в `internal/orders/retry.go:88` может потерять событие примерно после 3 повторных попыток.
```

Gate output: `ex4-after.md: 0 hard, 0 soft (source en)`.

#### Example 5: a domain word that stays

In accounting code, the name of the legs of a double-entry posting is «ноги». The gate identifies this sense as different from the CI slang.

```text
Обработчик `PostTransfer` пишет обе ноги проводки в одной транзакции: `debit` и `credit`.
```

Gate output:

```text
soft domain      1:37      «ноги»  учётные «ноги» проводки (legs): оставить, если так в коде; иначе «сумма `send`/`recv`», «сторона base/quote»
ex5.md: 0 hard, 1 soft
```

## Data model

In the two skills, the JSON tables are the source of truth. The gate embeds the JSON with `go:embed`. Edit the JSON files directly.

| skill | data |
|---|---|
| antislop-eng | `scripts/dictionary.json` |
| antislop-rus | `scripts/data/lexicon.json`, `glossary.json`, `hedges.json` |

## Testing

No `go.mod` file is necessary. Only Go 1.22 or higher is necessary.

In the root directory of the repository, examine the format of the Go files:

```sh
gofmt -l .
```

From the root directory, do the checks for antislop-eng:

```sh
cd skills/antislop-eng
go vet scripts/check.go scripts/check_test.go
cd scripts && go test check.go check_test.go
```

From the root directory, do the checks for antislop-rus:

```sh
cd skills/antislop-rus
go vet scripts/antislop.go scripts/antislop_test.go
cd scripts && go test antislop.go antislop_test.go
```

Each `go test` command starts the test suite of one skill.

## Known limits

### antislop-eng

- The checker is a heuristic, not a parser. The writer must make a decision about each finding with the mark `?` (passive or adjective, -ing form or technical noun, noun cluster).
- The checker does not examine meaning, one topic in each sentence, the approved sense of a word or an ambiguous pronoun. The agent reads the text for these items at the end of the skill procedure.
- A glossary term stops all checks on that word. But if a term has a non-approved verb sense (log, build, branch), the text must use the approved verb. The checker cannot see the verb use.
- De-inflection can report a word with a suffix as the word without the suffix. For example, the checker reports *caller* as `call` unless the glossary contains `caller`.
- Some dictionary rows contain incorrect text from an adjacent entry. Some alternatives in the non-approved table are not complete.
- The hedge hints include *may, might, perhaps, likely, probably, probable, unlikely, should*. The checker does not find other hedges.

### antislop-rus

- The gate counts the hedges of each strength. A missing hedge and one more hedge of the same strength at a different location give the same count. In this condition, the gate does not see the missing hedge. The fact ledger is for this problem.
- The glossary check operates only with an English `--source`. On a text that is only in Russian, only the lexicon finds Cyrillized words.
- If the two tables do not contain a word, the gate accepts the word. For example, the gate accepts «на стейджинге» and the adjective «красным». Only the full-text pass of the skill procedure finds these words.
- The gate does not examine grammar, word sequence or meaning. The gate does not find a new fact that has no number, no identifier and no certainty word.
- If the source is in Russian, the gate does not compare `advice` and `must` hedges. The cause is that the Russian words «стоит» and «должен» have too many other senses.
- `scripts/testdata/slop_cases.json` contains 20 review comments for the tests. A person manually identified each slop item in these comments with a label. The gate finds all 84 slop items that have a label, and each of the 87 hard findings agrees with a label. The author wrote these test cases for this version of the lexicon. The test cases show that each rule operates as designed. The test cases do not give an estimate of the accuracy on text that is not in the test cases.

## Repository layout

```text
.
├── .github/workflows/ci.yml        gofmt, go vet, go test (branch dev)
├── README.md
└── skills/
    ├── antislop-eng/
    │   ├── SKILL.md
    │   ├── CHANGELOG.md
    │   └── scripts/
    │       ├── check.go                  the checker
    │       ├── check_test.go
    │       ├── dictionary.json           the approved-word dictionary, embedded
    │       ├── glossary.example.txt
    │       └── glossary/software.txt     built-in software glossary, embedded
    └── antislop-rus/
        ├── SKILL.md
        └── scripts/
            ├── antislop.go               the gate
            ├── antislop_test.go
            ├── data/*.json               lexicon, glossary and hedge tables, embedded
            └── testdata/slop_cases.json
```

