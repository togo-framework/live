# Coder template: a "live-agent" workspace that boots a togo live agent.
#
# On start it runs agent-boot.sh (register + write .mcp.json) then agent-loop.sh,
# so the workspace *is* a live agent — claude/omni behind Coder auth, answering
# prompts your togo app pushes. Provision one per agent:
#
#   coder templates push live-agent -d deploy/coder-template
#   coder create ada --template live-agent
#
# This is the template the togo-framework/coder exec provider targets via
# CODER_TEMPLATE, so `providers.Use(exec=coder)` can spin agents up on demand.
terraform {
  required_providers {
    coder  = { source = "coder/coder" }
    docker = { source = "kreuzwerker/docker" }
  }
}

variable "live_api_url" {
  description = "Base URL of your togo app (exposes /api/live)."
  type        = string
}

data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_agent" "main" {
  arch = "amd64"
  os   = "linux"

  # The agent's persona + which backend it runs.
  env = {
    LIVE_API_URL        = var.live_api_url
    LIVE_AGENT_NAME     = data.coder_workspace.me.name
    LIVE_AGENT_PROVIDER = "claude"
    LIVE_WORK_DIR       = "/home/coder/live-agent"
    # ANTHROPIC_API_KEY should come from a Coder parameter or the base image's
    # pre-authenticated `claude login`. Do not hardcode secrets here.
  }

  startup_script = <<-EOT
    set -e
    # scripts/ is baked into the image at /opt/live (see Dockerfile note below),
    # or fetched from the plugin repo. Register, then run the answer loop.
    bash /opt/live/agent-boot.sh
    nohup bash /opt/live/agent-loop.sh > /home/coder/live-agent.log 2>&1 &
  EOT
}

# A minimal container to host the agent. Swap the image for one that ships
# go+node+claude+omni (and the deploy/scripts at /opt/live) for a fast cold start.
resource "docker_image" "agent" {
  name = "codercom/enterprise-base:ubuntu"
}

resource "docker_container" "workspace" {
  count      = data.coder_workspace.me.start_count
  image      = docker_image.agent.name
  name       = "live-${data.coder_workspace_owner.me.username}-${data.coder_workspace.me.name}"
  entrypoint = ["sh", "-c", coder_agent.main.init_script]
  env        = ["CODER_AGENT_TOKEN=${coder_agent.main.token}"]
}
