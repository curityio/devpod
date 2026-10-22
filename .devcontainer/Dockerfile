# Built from .github/devcontainer by .github/workflows/build-devcontainer-image.yaml.
FROM ghcr.io/loft-sh/devpod:dev-6b64450abdb0ebc2ce7f663f9ff935c56679f8b6

# Claude is installed here rather than by a dev container feature, and its configuration
# lives in one directory that is part of the image: the login comes from
# CLAUDE_CODE_OAUTH_TOKEN, and these files hold everything else (onboarding and folder
# trust, the Atlassian MCP server, permissions, what the container's Claude remembers).
USER devpod
ENV CLAUDE_CONFIG_DIR=/home/devpod/.claude \
    PATH=/home/devpod/.local/bin:$PATH
RUN curl -fsSL https://claude.ai/install.sh | bash

COPY --chown=devpod:devpod .claude.json /home/devpod/.claude/.claude.json
COPY --chown=devpod:devpod claude-settings.json /home/devpod/.claude/settings.json
COPY --chown=devpod:devpod claude-memory.md /home/devpod/.claude/CLAUDE.md
# GitHub's and Bitbucket's published host keys, pinned rather than fetched with ssh-keyscan
# at start, which would trust whatever answered.
# ~/.ssh is made first, so that devpod owns it and ssh can update known_hosts.
RUN mkdir -m 700 -p /home/devpod/.ssh
COPY --chown=devpod:devpod known_hosts /home/devpod/.ssh/known_hosts
USER root
