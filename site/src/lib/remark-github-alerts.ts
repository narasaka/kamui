import type { Paragraph, Root } from "mdast";
import { visit } from "unist-util-visit";

const TITLES = {
  NOTE: "Note",
  TIP: "Tip",
  IMPORTANT: "Important",
  WARNING: "Warning",
  CAUTION: "Caution",
} as const;

const MARKER = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*/;

/** Renders GitHub's `> [!WARNING]` blockquotes as titled callouts. */
export function remarkGithubAlerts() {
  return (tree: Root) => {
    visit(tree, "blockquote", (node) => {
      const first = node.children[0];
      if (first?.type !== "paragraph") return;
      const text = first.children[0];
      if (text?.type !== "text") return;
      const match = MARKER.exec(text.value);
      if (!match) return;

      const kind = match[1] as keyof typeof TITLES;
      text.value = text.value.slice(match[0].length);
      if (!text.value) first.children.shift();

      const title: Paragraph = {
        type: "paragraph",
        data: { hProperties: { className: ["alert-title"] } },
        children: [{ type: "text", value: TITLES[kind] }],
      };
      node.children.unshift(title);
      node.data = {
        ...node.data,
        hName: "div",
        hProperties: { className: ["alert", `alert-${kind.toLowerCase()}`], role: "note" },
      };
    });
  };
}
