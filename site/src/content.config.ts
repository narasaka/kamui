import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { DOCS } from "./lib/docs";

export const collections = {
  docs: defineCollection({
    loader: glob({ pattern: DOCS.map((doc) => `${doc.slug}.md`), base: "../docs" }),
  }),
};
