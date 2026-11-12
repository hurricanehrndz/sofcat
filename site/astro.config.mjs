// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightLinksValidator from "starlight-links-validator";

const repo = "https://github.com/hurricanehrndz/sofcat";

export default defineConfig({
  site: "https://sofcat.app",
  redirects: { "/guides/zip-items": "/zip-items/overview/" },
  integrations: [
    starlight({
      title: "SofCat",
      description: "A brandable catalog for anything you can put on disk or run.",
      // The storefront's own 64 px mark; Starlight serves the logo unresized.
      logo: { src: "../ui/frontend/public/sofcat-icon.png" },
      favicon: "/favicon.png",
      social: [{ icon: "github", label: "GitHub", href: repo }],
      editLink: { baseUrl: `${repo}/edit/main/site/` },
      customCss: ["./src/styles/custom.css"],
      // The storefront demo is copied into dist/demo/ after the Astro build
      // (`just site`), so the validator cannot see it.
      plugins: [starlightLinksValidator({ exclude: ["/demo/", "/demo/**"] })],
      sidebar: [
        {
          label: "Start here",
          items: ["download", "getting-started", "configuration"],
        },
        {
          label: "Guides",
          items: ["guides/nupkg-items", "guides/required-software", "guides/conditions", "guides/osquery-inventory"],
        },
        {
          label: "Zip items",
          items: ["zip-items/overview", "zip-items/placements", "zip-items/placement-files", "zip-items/tree-templates", "zip-items/building"],
        },
        {
          label: "AI store",
          items: ["ai-store/overview", "ai-store/skills", "ai-store/plugins", "ai-store/config"],
        },
        {
          label: "Reference",
          items: ["reference/data-directory", "reference/event-log", "reference/code-signing"],
        },
        {
          label: "Contributing",
          items: ["contributing/dev"],
        },
      ],
    }),
  ],
});
