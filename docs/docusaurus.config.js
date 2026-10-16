__webpack_public_path__ = "/devpod/";

module.exports = {
  title: "DevPod docs | DevContainers everywhere",
  tagline: "DevContainers everywhere",
  url: "https://curityio.github.io",
  baseUrl: __webpack_public_path__,
  favicon: "/media/devpod-favicon.svg",
  organizationName: "curityio", // Usually your GitHub org/user name.
  projectName: "devpod", // Usually your repo name.
  themeConfig: {
    colorMode: {
      defaultMode: "light",
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      logo: {
        alt: "devpod",
        src: "/media/devpod-logo-horizontal.svg",
        srcDark: "/media/devpod-logo-horizontal-dark.svg",
      },
      items: [
        {
          to: "/what-is-devpod",
          label: "Docs",
          position: "left",
        },
        {
          href: "https://github.com/curityio/devpod",
          className: "github-link",
          "aria-label": "GitHub",
          position: "right",
        },
      ],
    },
    footer: {
      style: "light",
      links: [],
      copyright: `Copyright © ${new Date().getFullYear()} <a href="https://curity.io/">Curity AB</a>`,
    },
  },
  presets: [
    [
      "@docusaurus/preset-classic",
      {
        docs: {
          path: "pages",
          routeBasePath: "/",
          sidebarPath: require.resolve("./sidebars.js"),
          editUrl: "https://github.com/curityio/devpod/edit/main/docs/",
        },
        theme: {
          customCss: require.resolve("./src/css/custom.css"),
        },
      },
    ],
  ],
  plugins: [],
  scripts: [
    {
      src: "https://cdnjs.cloudflare.com/ajax/libs/clipboard.js/2.0.0/clipboard.min.js",
      async: true,
    },
    {
      src: __webpack_public_path__ + "js/custom.js",
      async: true,
    },
  ],
};
