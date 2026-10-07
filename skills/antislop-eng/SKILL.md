---
name: antislop-eng
description: "Write or rewrite English in controlled technical English: technical documentation, manuals, installation and operating procedures, maintenance steps, safety warnings, troubleshooting guides, AND English code-review comments, MR/PR feedback, incident and status reports, tool descriptions, error messages and other engineering text that a non-native reader or an AI agent must not misread. Use when the user mentions controlled language, plain technical English, or asks to make technical text unambiguous. Bundles an approved-word dictionary, the writing rules and a Go checker. Not for marketing, creative or conversational copy."
metadata:
  version: "2.1.0"
---

# Controlled technical English

The rules remove choice: one approved word per meaning, one part of speech per word, one instruction per sentence, active voice, simple tenses, hard length limits. The dictionary decides, and a script checks the text against it.

## Commands

Run every command with this path. The checker is Go, standard library only. `go run` needs no go.mod.

- Look up a word: `go run ~/.claude/skills/antislop-eng/scripts/check.go --lookup WORD [WORD ...]`
- Check a text: `go run ~/.claude/skills/antislop-eng/scripts/check.go draft.md --glossary terms.txt [--mode procedural|descriptive] [--json]`
- Check stdin: `... check.go - --glossary terms.txt`

The exit code is 0 even with findings. Read the report. Code fences, indented code and inline `code` spans are not checked; a span counts as one word.

## Modes

**Strict** — procedures, instructions, safety text, error messages, tool descriptions, inter-agent instructions. Every rule applies, the dictionary included. Replace every non-approved word.

**Relaxed** — explanations, code-review comments, MR descriptions, design notes, status reports. Structure rules apply in full. The checker still reports dictionary hits, and you judge each one. Replace a hit when the approved word keeps the meaning and reads naturally. Keep it when the replacement changes or blurs the meaning. The hedge table applies in both modes.

Pick the mode from the text type. Never ask for a procedure: strict is the answer.

## Workflow

1. **Classify each block.** Procedural (tells the reader to do something): imperative, max 20 words per sentence. Descriptive: max 25 words, max 6 sentences per paragraph, one topic per paragraph. In a code-review comment, the requested change is procedural and the reason is descriptive.

2. **Build the glossary.** The dictionary holds general words. Domain words are technical nouns (Rule 1.5) or technical verbs (Rule 1.12) only if they fit a category: names, materials, tools, processes, quoted labels, subject-field instructions. Look up each candidate first. Do not add a word the dictionary already approves.
   - Auto-glossary needs no file: code spans, paths, `file.go:12`, snake_case, camelCase, dotted names, `--flags`, URLs and short ALLCAPS acronyms are accepted and listed in the report. Check that list.
   - The software glossary (`scripts/glossary/software.txt`: MR, CI, pipeline, commit, timeout, cache …) is built in and always loaded. Put only the project's extra terms (one per line, max 3 words, one name per item) in a file and pass it with `--glossary`; it adds to the built-in list. `--no-default-glossary` turns the built-in list off.
   - A glossary term turns off every check on that word. A term with a non-approved verb sense (log, build, branch) still needs the approved verb: log (v) → RECORD.
   - A technical noun is never a verb: *apply grease*, not *grease the bolt*.

3. **Draft.** Condition first, then the command. One action per sentence. Repeat the noun, do not use a pronoun that can point at two things. Run `--lookup` before you guess a word. Many alternatives need a new sentence construction, not a word swap (Rule 9.1).

4. **Check loop.** Draft → check → fix → check again.
   - Fix every non-approved word, every unknown word (glossary or replace), and every hard finding: length, semicolon, contraction, perfect tense, -ing verb, Latin abbreviation, he/she, paragraph over 6 sentences.
   - Findings marked `?` (passive or adjective, -ing or technical noun, noun cluster) need your judgement.
   - Stop when the only remaining items are ones you can justify. Then read for what no script checks: one topic per sentence, condition first, the approved meaning of each word, ambiguous *with*, *this* and pronouns.

5. **Output** (see Output).

## Hedge table

A hedge is content. Keep its strength exactly. Never promote "may have failed" to "failed".

| Source strength | Approved wording |
|---|---|
| possibility: may, might, could, perhaps | POSSIBLY, or IT IS POSSIBLE THAT ... |
| probable: likely, probably | IT IS VERY POSSIBLE THAT ... |
| certainty: will, definitely, certainly | plain statement |
| should = advice | WE RECOMMEND THAT ... |
| should = obligation | MUST |
| ability or permission | CAN |

If you cannot tell advice from obligation, keep the weaker one (WE RECOMMEND). A compound tense that holds a hedge ("the service may have stopped") becomes "IT IS POSSIBLE THAT THE SERVICE STOPPED". The claim stays a possibility.

## Core rules

- **Words (1.1–1.3, 9.x).** Only approved words, technical nouns and technical verbs. One meaning, one part of speech: *test* is a noun only (*do a test*). No phrasal verbs (*set up* → *install*, *find out* → *find*). An approved verb over a noun phrase (*examine the log*, not *do an examination of*). American spelling.
- **Verbs (3.x).** Infinitive, imperative, simple present, simple past, simple future, past participle as adjective. No *has/have/had + participle*, no *is + -ing*, no *been*, no *being*. -ing only in a technical noun or heading. Active voice; passive only in descriptive text when the agent is unknown.
- **Sentences (2.x, 4.x, 5.x, 8.x).** Word count per 8.4–8.7: a number with its unit, an identifier, a hyphenated word, a quoted label, a code span count as one word. One instruction per sentence. Condition first, then a comma, then the command. No semicolons. No contractions. No dropped articles, subjects or verbs. Noun clusters max 3 words: break longer ones with *of*, *for*, *that*. Lists for 3 or more items.
- **Notes and safety (5.5, 7.x).** A note gives information only. WARNING = injury or death, CAUTION = damage: command or condition first, then the consequence.
- **Clarity (GR-1 to GR-8).** Keep *that* after *make sure*, *show*, *recommend*. No Latin abbreviations (*e.g.* → *for example*). No he/she.

## Preserve the meaning

The rewrite changes the form, never the content.
- Keep every condition, limit, quantity, scope qualifier and exception. If a limit forces a cut, split the sentence instead.
- Keep modality (see Hedge table).
- Never add a fact: no new cause, frequency, mechanism, step or consequence. If a safety instruction has no stated consequence, write the command and flag the gap.
- Do not alter quoted text, screen text, identifiers, code, commands, numbers or units.

## Output

**Default: the rewritten text, and nothing else.** No preamble, mode line, violation count, change summary or closing offer.

One permitted addition: if you kept a longer phrasing or a non-approved word on purpose, add one line after the text: `Kept as-is: <phrase> — <the precision a change would lose>`. Omit it when there is nothing to report.

**On request** ("show the changes", "which rules", "compliance notes", "review this against the rules"): give a table `Rule | Original | Rewrite`, most serious first (dictionary and safety, then structure, then style). Then the mode, the glossary terms you accepted with their category, and any sentence left over the limit with the reason.

If the input already complies, say so. Do not force changes.

## Boundaries

**Will:** rewrite into short, single-meaning, active sentences; keep every fact, condition and qualifier; keep the strength of every hedge; use the bundled dictionary as the authority; suggest a glossary entry for a domain term that must stay.

**Will not:**
- Convert "may have failed" into "failed", or "could be caused by X" into "X is the cause".
- Add a claim the source did not make.
- Drop a safety condition, exception or scope qualifier to meet a word limit.
- Rewrite creative, marketing or persuasive copy.
- Make weak content true or useful. A hollow paragraph stays hollow; say so.
- Shorten past clarity. The goal is no ambiguity, not the fewest words.

## Files

- `scripts/check.go` — the checker, one Go file, standard library only.
- `scripts/dictionary.json` — the approved-word dictionary, the source of truth. Edit it directly.
- `scripts/glossary/software.txt` — starter glossary for software and code-review English.
- `scripts/check_test.go` — tests: `cd scripts && go test check.go check_test.go` (no go.mod needed).
