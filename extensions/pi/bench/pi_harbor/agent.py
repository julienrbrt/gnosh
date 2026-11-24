"""Harbor agent: pi, optionally with the pi-gnosh extension, in a set context window.

Both benchmark arms use this one class, so everything but the extension is the same:
pi version, model, reasoning effort, context window and pi's compaction settings.

    -a pi_harbor.agent:PiArm --ak version=1.6.0 --ak gnosh=false --ak context_window=65536
    -a pi_harbor.agent:PiArm --ak version=1.6.0 --ak gnosh=true  --ak context_window=65536
"""
from __future__ import annotations

import json
import os
import shlex
from pathlib import Path
from typing import override

from pydantic import Field

from harbor.agents.installed.pi import Pi, PiOptions, _REMOTE_PI_CONFIG_DIR
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

_REMOTE_EXT = "/tmp/pi-gnosh"


class PiArmOptions(PiOptions):
    gnosh: bool = Field(default=False, description="Load the pi-gnosh extension (gnosh's mem stack and slide).")
    context_window: int = Field(default=131072, ge=4096, description="contextWindow pi is told the model has.")
    max_output: int = Field(default=32768, ge=1024, description="maxTokens of the model in models.json.")
    reserve_tokens: int = Field(default=16384, ge=0, description="pi compaction.reserveTokens.")
    keep_recent_tokens: int = Field(default=20000, ge=0, description="pi compaction.keepRecentTokens.")
    ext_dir: str = Field(default=str(Path(__file__).resolve().parents[2] / "src"), description="Host path of the extension's src folder.")
    gnosh_binary: str = Field(default=os.environ.get("GNOSH_BIN", "gnosh"), description="Host path of a static (CGO_ENABLED=0) linux gnosh with `gnosh bridge`; default $GNOSH_BIN.")


class PiArm(Pi):
    options_model = PiArmOptions
    options: PiArmOptions

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        await super().install(environment)
        if not self.options.gnosh:
            return
        await environment.upload_dir(self.options.ext_dir, f"{_REMOTE_EXT}/src")
        await environment.upload_file(self.options.gnosh_binary, f"{_REMOTE_EXT}/gnosh")
        await self.exec_as_root(
            environment,
            # On PATH: pi loads the extension before it reads its flags.
            command=f"chmod -R a+rX {_REMOTE_EXT} && install -m 755 {_REMOTE_EXT}/gnosh /usr/local/bin/gnosh && gnosh --version",
        )

    @override
    def _build_custom_models_json(self, access, model_id):
        out = super()._build_custom_models_json(access, model_id)
        if out:
            for p in out.get("providers", {}).values():
                for m in p.get("models", []):
                    m["contextWindow"] = self.options.context_window
                    m["maxTokens"] = self.options.max_output
        return out

    @override
    def build_cli_flags(self) -> str:
        flags = super().build_cli_flags()
        if self.options.gnosh:
            flags = (flags + " " if flags else "") + f"--extension {_REMOTE_EXT}/src/index.ts"
        return flags

    @override
    async def run(self, instruction: str, environment: BaseEnvironment, context: AgentContext) -> None:
        settings = {
            "compaction": {
                "enabled": True,
                "reserveTokens": self.options.reserve_tokens,
                "keepRecentTokens": self.options.keep_recent_tokens,
            }
        }
        await self._ensure_config_dir(environment)
        path = (_REMOTE_PI_CONFIG_DIR / "settings.json").as_posix()
        text = json.dumps(settings, indent=2) + "\n"
        await self.exec_as_agent(environment, command=f"printf %s {shlex.quote(text)} > {shlex.quote(path)}")
        await super().run(instruction, environment, context)
