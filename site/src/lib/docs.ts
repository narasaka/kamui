export const REPO_URL = "https://github.com/narasaka/kamui";

// Docs published on the site, in navigation order. Each slug names a file in
// the repository's docs/ directory; the site renders that file at build time.
export const DOCS = [
  {
    slug: "how-it-works",
    label: "How it works",
    description: "The SSH connection, port mirroring, reconnects, and the dedicated browser.",
  },
  {
    slug: "install",
    label: "Install",
    description: "Installing, upgrading, and uninstalling the CLI and the menu bar app.",
  },
  {
    slug: "cli",
    label: "CLI",
    description: "Every command, flag, and exit code.",
  },
  {
    slug: "configuration",
    label: "Configuration",
    description: "The optional config file and the port policy.",
  },
  {
    slug: "menu-bar-app",
    label: "Menu bar app",
    description: "Sessions and ports from the macOS menu bar.",
  },
] as const;
