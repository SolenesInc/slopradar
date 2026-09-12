<!-- slopradar -->
## slopradar

```diff
+ 0.000 source mass added to functions over CC 10
- 0.000 source mass removed from functions over CC 10
± 0 clone pairs (0 introduced, 0 removed)
```

| bucket | mass added over CC 10 | mass removed over CC 10 | clone lines in touched files |
|---|---:|---:|---:|
| source | +0.000 | -0.000 | 0 → 0 |
| tests | +36.000 | -0.000 | 4 → 5 |

<details>
<summary>1 functions changed mass</summary>

| function | bucket before → after | CC before → after | SLOC before → after | Δmass | note |
|---|---|---:|---:|---:|---|
| <code>TestOnly</code> in <code>only_test.go</code> | · → tests | · → 12 | · → 9 | +36.000 | new |

</details>

<details>
<summary>0 clone pairs introduced, 0 removed</summary>

```text
```

</details>

<details>
<summary>How to read these numbers</summary>

Cyclomatic complexity (CC) counts decision paths through a function. SLOC is its non-blank, non-comment source lines. Mass is CC × √SLOC; erosion is the share of repository function mass in functions with CC over 10. A clone pair is two ranges with the same tokens after comments are removed, while clone share is the share of source lines in such ranges. Lower erosion and clone share are generally easier to maintain, but duplication is not always wrong. Absolute erosion varies by language, so compare this repository against its own history. This report is information for the reviewer, not a merge gate.

</details>
