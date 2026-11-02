import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import GithubSlugger from "github-slugger";
import type { Root } from "mdast";
import { visit } from "unist-util-visit";
import { DOCS, REPO_URL } from "./docs";

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function headingIds(file: string): Set<string> {
  const slugger = new GithubSlugger();
  const ids = new Set<string>();
  let fenced = false;
  for (const line of readFileSync(file, "utf8").split("\n")) {
    if (line.startsWith("```")) fenced = !fenced;
    const heading = !fenced && /^#{1,6}\s+(.*)$/.exec(line);
    if (heading) {
      // Slug the heading's visible text, as Astro does: drop code ticks and link targets.
      const text = heading[1].replaceAll("`", "").replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1");
      ids.add(slugger.slug(text));
    }
  }
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
    visit(tree, (node) => {
      if (node.type !== "link" && node.type !== "definition") return;
      const href = node.url;
      if (!href || href.startsWith("#") || SCHEME.test(href)) return;

      const [target, hash] = href.split("#", 2);
      const relative = path.posix.normalize(path.posix.join("docs", target)).replace(/\/$/, "");
      const absolute = path.join(repoRoot, relative);
      if (relative.startsWith("..") || !existsSync(absolute)) {
        throw new Error(`docs link "${href}" points at a missing file`);
      }
      if (hash && relative.endsWith(".md") && !headingIds(absolute).has(hash)) {
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
