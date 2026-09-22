const fs = require("node:fs")

const {commentForReport, githubCommentMaxUTF16CodeUnits, marker} = require("./report-bounds.cjs")

const githubGraphQLMaxPageSize = 100

const commentsQuery = `query($owner: String!, $repo: String!, $number: Int!, $pageSize: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      headRefOid
      comments(first: $pageSize, after: $after) {
        nodes { databaseId url body author { __typename login } }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

function isSlopradarComment(comment) {
  return comment.author?.__typename === "Bot" && comment.author.login === "github-actions" && comment.body.startsWith(marker)
}

async function findSlopradarComment(github, request) {
  let after = null
  for (;;) {
    const {repository} = await github.graphql(commentsQuery, {...request, pageSize: githubGraphQLMaxPageSize, after})
    const {headRefOid, comments} = repository.pullRequest
    const existing = comments.nodes.find(isSlopradarComment)
    if (existing || !comments.pageInfo.hasNextPage) {
      return {head: headRefOid, existing}
    }
    after = comments.pageInfo.endCursor
  }
}

function isRateLimited(error) {
  if (error.errors?.some(({type}) => type === "RATE_LIMITED")) {
    return true
  }
  return (error.status === 403 || error.status === 429) && /rate limit/i.test(error.message)
}

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

  const expectedHead = pullRequest.head?.sha
  if (!expectedHead) {
    throw new Error("pull request event does not identify its head SHA")
  }
  const request = {
    owner: context.repo.owner,
    repo: context.repo.repo,
    number: pullRequest.number,
  }
  try {
    const {head, existing} = await findSlopradarComment(github, request)
    if (head !== expectedHead) {
      core.info(`slopradar comment skipped: report head ${expectedHead} differs from current PR head ${head}`)
      return
    }

    if (existing?.body === body) {
      core.info(`slopradar comment unchanged: ${existing.url}`)
      return
    }
    if (existing) {
      await github.rest.issues.updateComment({
        owner: request.owner,
        repo: request.repo,
        comment_id: existing.databaseId,
        body,
      })
      core.info(`slopradar comment updated: ${existing.url}`)
      return
    }

    const created = await github.rest.issues.createComment({
      owner: request.owner,
      repo: request.repo,
      issue_number: request.number,
      body,
    })
    core.info(`slopradar comment created: ${created.data.html_url}`)
  } catch (error) {
    if (isRateLimited(error)) {
      core.warning(
        `slopradar comment skipped: the GitHub API rate limit is exhausted. The report is in the job summary: ${runUrl}. ${error.message}`,
      )
      return
    }
    const status = error.status ? ` HTTP ${error.status}` : ""
    throw new Error(
      `slopradar could not upsert the pull request comment.${status}; ensure the workflow grants pull-requests: write. ${error.message}`,
      {cause: error},
    )
  }
}
