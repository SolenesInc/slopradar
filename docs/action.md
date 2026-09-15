# The GitHub Action

`SolenesInc/slopradar` is a composite action. It builds slopradar from its own
checkout, runs `slopradar diff` between the pull request's base branch and its
head, writes the report to the job summary, and keeps one comment on the pull
request up to date. The numbers never fail the job.

## Workflow

```yaml
name: slopradar

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

concurrency:
  group: slopradar-${{ github.event.pull_request.number }}
  cancel-in-progress: false

jobs:
  slopradar:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: SolenesInc/slopradar@v0.1.0
        with:
          trend-months: "12"
          comment: "true"
```

The concurrency group serializes report runs per pull request, so keep it when
copying the workflow. The comment needs `pull-requests: write`.

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `base` | the pull request's base branch | Branch the report compares against, fetched as `origin/<base>` |
| `trend-months` | `"0"` | Months of first-parent history charted after the diff; `0` skips the trend |
| `comment` | `"true"` | Create or update the pull request comment |

## What a run does

The action builds the binary from the same checkout that supplied its scripts,
so a moved ref or fork cannot mix code from two sources, and puts it on the
job's `PATH`.

It then fetches the base branch into `refs/remotes/origin/<base>`, unshallowing
the checkout when `actions/checkout` produced a shallow clone, and runs:

```sh
slopradar diff --base refs/remotes/origin/<base> --head HEAD --format md [--trend <months>]
```

`diff` compares from the merge base of the two revisions, so commits already on
the base branch do not appear in the report. With `trend-months`, the report
ends with a chart of erosion by month for each bucket. Each point is the
repository at the end of its month, and the last point is the pull request
head.

## The comment

The action always writes the report to the job summary. With `comment` enabled
it creates one comment on the pull request, marked so later runs find it, and
updates that same comment on every later push. A run for an older pull request
head skips comment publication and says so in the job log, so a fast second
push cannot overwrite a newer report with an older one.

On a fork pull request, or a
[Dependabot pull request whose token is read-only](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-on-actions#restrictions-when-dependabot-triggers-events),
the action keeps the summary and says in the job log that it skipped the
comment. Other API failures stay visible.

## Large reports

Reports within GitHub's
[65,536 UTF-16-code-unit comment capacity](https://github.com/github/branch-deploy/blob/826fd07b4eae2e0a6b750c2a19d68b6c8d509cc0/__tests__/functions/truncate-comment-body.test.ts)
appear in full. Above it, the comment keeps the complete headline-and-bucket
prefix when it fits, then names the capacity and actual size and links the
workflow run. If that prefix is itself too large, the comment falls back to a
short linked notice.

If a report exceeds GitHub's
[1 MiB job-summary capacity](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands#step-isolation-and-limits),
the action uploads the untouched Markdown as an artifact and posts a compact
linked summary. It never cuts through a table or code fence. The
[boundary tests](../scripts/report-bounds.test.cjs) cover the exact edges.

## Pinning

`SolenesInc/slopradar@v0.1.0` selects a release. A tag can be moved, so a
workflow that must run the same analyzer every time names the full commit SHA,
as in `SolenesInc/slopradar@<sha>`. `SolenesInc/slopradar@main` follows the
repository, so report changes land on the next run without a workflow edit.
The action builds from whichever checkout the ref selects. The standalone
`go install` command in the [README](../README.md#command-line) is versioned
separately.
