import { expect, test } from "bun:test";
import { unpinnedUses } from "./check-action-pins";

test("rejects tags, branches, short SHAs, and unpinned reusable workflows", () => {
  const document = Bun.YAML.parse(`
jobs:
  reusable:
    uses: owner/repo/.github/workflows/build.yml@main
  build:
    steps:
      - uses: actions/checkout@v6
      - 'uses': owner/action@main
      - uses: owner/action@abcdef0
      - uses: docker://alpine:latest
`);
  expect(unpinnedUses(document)).toHaveLength(5);
});

test("accepts full pins and local actions without matching shell text", () => {
  const sha = "a".repeat(40);
  const document = Bun.YAML.parse(`
jobs:
  reusable:
    uses: owner/repo/.github/workflows/build.yml@${sha}
  build:
    steps:
      - &checkout
        uses: actions/checkout@${sha} # v6
      - *checkout
      - uses: ./local-action
      - uses: docker://alpine@sha256:${"b".repeat(64)}
      - run: 'echo "uses: owner/action@main"'
`);
  expect(unpinnedUses(document)).toEqual([]);
});
