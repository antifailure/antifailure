# The context one documentation answer costs

Run on 2026-09-08T01:12:06Z UTC.

- Machine: darwin arm64, 8 cores.
- Harness: `just benchmark`, which is `engine/internal/docs/benchmark_test.go` in this repository.
- Corpus: the 92 pages this build ships, embedded by `tools/docsembed`.
- Question: "what does stance mean", answered by `search_documentation` at its default budget of 4000 characters.

| What an agent reads | Characters | Times the answer |
| --- | --- | --- |
| The answer: 5 excerpts from 5 pages | 1,735 | 1x |
| The table of contents, path and title per page | 3,588 | 2x |
| The whole page the answer is on, reference/manifest.md | 24,787 | 14x |
| The whole documentation set, 92 pages | 1,114,052 | 642x |

The answer is **0.16 percent** of the documentation set and **7.0 percent** of the one page it came from.

## Which sections it named, and what it said it left out

- `reference/manifest.md` at `#the-stances`, 257 characters. Manifest reference > `datastores` > The stances
- `reference/mcp.md` at `#the-tools`, 360 characters. MCP server > The tools
- `concepts/inventory.md` at `#requiring-a-dimension`, 434 characters. Inventory > Requiring a dimension
- `concepts/masking.md` at `#more-than-one-store`, 371 characters. Masking > More than one store
- `contributing/provider-authoring.md` at `#writing-a-datastore`, 313 characters. Writing a provider > Writing a datastore

6 pages matched and 5 were returned. 1 were named as not shown.

## Characters, and how to read them as tokens

Characters are what this harness measures, because they need no tokenizer, no network and no dependency, and because a customer running this gets the same number on the same corpus every time. A tokenizer is a fixed factor on top, so the ratios in the table above are the same whichever one you use.

To count tokens over the exact bytes this measured rather than over something similar, run the test again with a payload directory and count the files it writes:

```sh
AF_DOCS_BENCHMARK_OUT=/tmp/report.md \
AF_DOCS_BENCHMARK_PAYLOADS=/tmp/payloads \
  go test ./internal/docs -run TestBenchmarkTheContextOneAnswerCosts -count=1

python3 -c 'import sys,tiktoken; e=tiktoken.get_encoding("cl100k_base"); print(len(e.encode(open(sys.argv[1]).read())))' /tmp/payloads/answer-excerpts.txt
```
