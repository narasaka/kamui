import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import type { Root } from "mdast";
import { createMarkdownProcessor } from "@astrojs/markdown-remark";
import { fromMarkdown } from "mdast-util-from-markdown";
import { visit } from "unist-util-visit";
import { headingIds, remarkDocLinks } from "./remark-doc-links.ts";

const repoRoot = fileURLToPath(new URL("../../../", import.meta.url));

function rewrite(markdown: string): string[] {
  const tree: Root = fromMarkdown(markdown);
  remarkDocLinks({ repoRoot })(tree);
  const urls: string[] = [];
  visit(tree, "link", (link) => {
    urls.push(link.url);
  });
  return urls;
}

test("links to published docs become site routes", () => {
  assert.deepEqual(rewrite("[a](how-it-works.md#reconnecting) [b](configuration.md)"), [
    "/docs/how-it-works#reconnecting",
    "/docs/configuration",
  ]);
});

test("links to other repository files go to GitHub", () => {
  assert.deepEqual(rewrite("[a](decisions/) [b](../README.md)"), [
    "https://github.com/narasaka/kamui/tree/main/docs/decisions",
    "https://github.com/narasaka/kamui/blob/main/README.md",
  ]);
});

test("same-page anchors must name a heading on the page", () => {
  assert.deepEqual(rewrite("# Valid heading\n\n[ok](#valid-heading)"), ["#valid-heading"]);
  assert.throws(() => rewrite("# Valid heading\n\n[broken](#missing)"), /missing heading/);
});

test("missing files and headings fail", () => {
  assert.throws(() => rewrite("[a](nope.md)"), /missing file/);
  assert.throws(() => rewrite("[a](how-it-works.md#nope)"), /missing heading/);
});

test("heading ids follow the parsed Markdown", () => {
  const markdown = [
    "## Reconnecting ##",
    "",
    "Setext heading",
    "==============",
    "",
    "~~~sh",
    "# not a heading",
    "~~~",
    "",
    "## [Usage](cli.md) and `kamui foo`",
    "",
    "## Usage and kamui foo",
  ].join("\n");
  assert.deepEqual(
    headingIds(fromMarkdown(markdown)),
    new Set(["reconnecting", "setext-heading", "usage-and-kamui-foo", "usage-and-kamui-foo-1"]),
  );
});

test("heading ids match the ones Astro renders", async () => {
  const markdown = [
    "## Reconnecting ##",
    "Setext heading\n==============",
    "~~~sh\n# not a heading\n~~~",
    "## [Usage](cli.md) and `kamui foo`",
    "## Usage and kamui foo",
    "## Use <code>kamui</code>",
    "## Press <kbd>Ctrl</kbd> now",
    "## ![Logo](logo.png) Usage",
  ].join("\n\n");
  const processor = await createMarkdownProcessor({ syntaxHighlight: false });
  const { metadata } = await processor.render(markdown);
  assert.deepEqual(headingIds(fromMarkdown(markdown)), new Set(metadata.headings.map((heading) => heading.slug)));
});
