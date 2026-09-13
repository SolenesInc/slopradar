const fs = require("node:fs")

const {commentForReport, githubCommentMaxUTF16CodeUnits, marker} = require("./report-bounds.cjs")

module.exports = async function upsertComment({
  github,
  context,
  core,
  reportPath,
  commentEnabled,
  runUrl,
}) {
  if (commentEnabled !== "true" && commentEnabled !== "false") {
    throw new Error(`comment must be true or false, got: ${commentEnabled}`)
  }
  if (commentEnabled === "false") {
    core.info("slopradar comment skipped: comment=false")
    return
  }

  const pullRequest = context.payload.pull_request
  if (!pullRequest) {
    core.info("slopradar comment skipped: this is not a pull request event")
    return
  }

  const repository = `${context.repo.owner}/${context.repo.repo}`
  const headRepository = pullRequest.head?.repo?.full_name
  if (headRepository !== repository) {
    core.info(
      `slopradar comment skipped: pull request head is ${headRepository || "an unavailable fork"}; GITHUB_TOKEN is read-only for fork pull requests`,
    )
    return
  }
  if (pullRequest.user?.login === "dependabot[bot]") {
    core.info(
      "slopradar comment skipped: GitHub gives Dependabot pull request workflows a read-only GITHUB_TOKEN",
    )
    return
  }

  const report = fs.readFileSync(reportPath, "utf8")
  const bounded = commentForReport(report, runUrl)
  if (bounded.overflow) {
    core.warning(
      `slopradar report exceeds the pull request comment limit: max_comment_utf16_code_units=${githubCommentMaxUTF16CodeUnits}, asked_comment_utf16_code_units=${bounded.asked}; linking the full workflow report`,
    )
  }
  const body = bounded.body

  const request = {
    owner: context.repo.owner,
    repo: context.repo.repo,
    issue_number: pullRequest.number,
  }
  try {
    const comments = await github.paginate(github.rest.issues.listComments, request)
    const existing = comments.find(
      (comment) => comment.user?.login === "github-actions[bot]" && comment.body?.startsWith(marker),
    )

    const expectedHead = pullRequest.head?.sha
    if (!expectedHead) {
      throw new Error("pull request event does not identify its head SHA")
    }
    const current = await github.rest.pulls.get({
      owner: request.owner,
      repo: request.repo,
      pull_number: pullRequest.number,
    })
    if (current.data.head.sha !== expectedHead) {
      core.info(`slopradar comment skipped: report head ${expectedHead} differs from current PR head ${current.data.head.sha}`)
      return
    }

    if (existing) {
      await github.rest.issues.updateComment({
        owner: request.owner,
        repo: request.repo,
        comment_id: existing.id,
        body,
      })
      core.info(`slopradar comment updated: ${existing.html_url}`)
      return
    }

    const created = await github.rest.issues.createComment({...request, body})
    core.info(`slopradar comment created: ${created.data.html_url}`)
  } catch (error) {
    const status = error.status ? ` HTTP ${error.status}` : ""
    throw new Error(
      `slopradar could not upsert the pull request comment.${status}; ensure the workflow grants pull-requests: write. ${error.message}`,
      {cause: error},
    )
  }
}
