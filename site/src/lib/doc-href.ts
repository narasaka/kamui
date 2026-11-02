import { getEntry, render } from "astro:content";

/** Links to a published doc page, failing the build if the doc or heading is missing. */
export async function docHref(slug: string, hash?: string): Promise<string> {
  const entry = await getEntry("docs", slug);
  if (!entry) throw new Error(`no published doc "${slug}"`);
  if (hash) {
    const { headings } = await render(entry);
    if (!headings.some((heading) => heading.slug === hash)) {
      throw new Error(`docs/${slug}.md has no heading "#${hash}"`);
    }
  }
  return `/docs/${slug}${hash ? `#${hash}` : ""}`;
}
