import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import GithubSlugger from "github-slugger";
import type { Root } from "mdast";
import { fromMarkdown } from "mdast-util-from-markdown";
import { toString } from "mdast-util-to-string";
import { visit } from "unist-util-visit";
import { DOCS, REPO_URL } from "./docs.ts";

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

/** Heading ids in a Markdown tree, slugged the way the rendered page names them. */
export function headingIds(tree: Root): Set<string> {
  const slugger = new GithubSlugger();
  const ids = new Set<string>();
  visit(tree, "heading", (heading) => {
    ids.add(slugger.slug(toString(heading)));
  });
  return ids;
}

/**
 * Rewrites relative links in docs/*.md for the site. Links to published docs
 * become site routes; links to other repository files go to GitHub. A link to a
 * missing file or heading fails the build, so the docs and the site can't drift.
 */
export function remarkDocLinks({ repoRoot }: { repoRoot: string }) {
  const published = new Map<string, string>(DOCS.map((doc) => [`docs/${doc.slug}.md`, doc.slug]));

  return (tree: Root) => {
    const ownIds = headingIds(tree);

    visit(tree, (node) => {
      if (node.type !== "link" && node.type !== "definition") return;
      const href = node.url;
      if (!href || SCHEME.test(href)) return;

      if (href.startsWith("#")) {
        if (href.length > 1 && !ownIds.has(href.slice(1))) {
          throw new Error(`docs link "${href}" points at a missing heading`);
        }
        return;
      }

      const [target, hash] = href.split("#", 2);
      const relative = path.posix.normalize(path.posix.join("docs", target)).replace(/\/$/, "");
      const absolute = path.join(repoRoot, relative);
      if (relative.startsWith("..") || !existsSync(absolute)) {
        throw new Error(`docs link "${href}" points at a missing file`);
      }
      if (hash && relative.endsWith(".md") && !headingIds(fromMarkdown(readFileSync(absolute, "utf8"))).has(hash)) {
        throw new Error(`docs link "${href}" points at a missing heading`);
      }

      const suffix = hash ? `#${hash}` : "";
      const slug = published.get(relative);
      if (slug) {
        node.url = `/docs/${slug}${suffix}`;
        return;
      }
      const kind = statSync(absolute).isDirectory() ? "tree" : "blob";
      node.url = `${REPO_URL}/${kind}/main/${relative}${suffix}`;
    });
  };
}
