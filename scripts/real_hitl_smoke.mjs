#!/usr/bin/env node

import fs from "node:fs";
import { promises as fsp } from "node:fs";
import net from "node:net";
import path from "node:path";
import { spawn } from "node:child_process";

const repoRoot = process.cwd();
const relayBinary = resolveBinary(process.env.AGENTUNNEL_REAL_HITL_RELAY_BINARY ?? "./bin/relay");
const agentunnelBinary = resolveBinary(process.env.AGENTUNNEL_REAL_HITL_BINARY ?? "./bin/agentunnel");
const ptyDriver = path.join(repoRoot, "scripts", "pty_driver.py");
const authUser = "demo";
const authPassword = "secret";
const agentToken = "agent-token";

const relayTail = createTailBuffer();

async function main() {
  await ensureExecutable(relayBinary, "relay");
  await ensureExecutable(agentunnelBinary, "agentunnel");

  const relayPort = await findFreePort();
  const relay = spawnProcess(
    relayBinary,
    ["--port", String(relayPort)],
    {
      AGENTUNNEL_BASIC_USER: authUser,
      AGENTUNNEL_BASIC_PASSWORD: authPassword,
      AGENTUNNEL_AGENT_TOKEN: agentToken,
    },
    relayTail,
  );
  await waitForHttp(`http://127.0.0.1:${relayPort}/healthz`, 15000, authHeader());
  console.log(`relay started on 127.0.0.1:${relayPort}`);

  const updates = await openEventSocket(`ws://${authUser}:${authPassword}@127.0.0.1:${relayPort}/api/updates/ws`);

  const filename = `.agentunnel-hitl-smoke-${Date.now()}.txt`;
  const filePath = path.join(repoRoot, filename);
  const prompt =
    `Use exactly one shell command: printf 'OK' > ${filename}. ` +
    `Do not run any other commands. After it succeeds, tell me you are done.`;

  const agent = await spawnPTYDriver(
    agentunnelBinary,
    ["codex", "--no-alt-screen", "-a", "untrusted", "-s", "workspace-write", prompt],
    {
      AGENTUNNEL_RELAY_ADDR: `127.0.0.1:${relayPort}`,
      AGENTUNNEL_RELAY_TOKEN: agentToken,
    },
  );

  try {
    const actionRequired = await updates.waitFor(
      (event) => event.type === "session_state" && event.state === "action_required",
      90000,
    );
    console.log("entered action_required:", actionRequired.session_id);

    const snapshotActionRequired = await waitForSessionInfo(
      relayPort,
      actionRequired.session_id,
      (session) => session.state === "action_required" && !!session.action_required_since,
      30000,
    );
    console.log("snapshot action_required_since:", snapshotActionRequired.action_required_since);

    await agent.send("y");

    const resumedOutput = await updates.waitFor(
      (event) => event.session_id === actionRequired.session_id && event.type === "output",
      30000,
    );
    console.log("received multiplexed output seq:", resumedOutput.seq);

    const backToNormal = await updates.waitFor(
      (event) =>
        event.session_id === actionRequired.session_id &&
        event.type === "session_state" &&
        event.state === "normal",
      90000,
    );
    console.log("returned to normal:", backToNormal.changed_at);

    const snapshotNormal = await waitForSessionInfo(
      relayPort,
      actionRequired.session_id,
      (session) => session.state === "normal" && !session.action_required_since,
      30000,
    );
    console.log("snapshot state:", snapshotNormal.state);

    await waitForFileContent(filePath, "OK", 30000);
    console.log(`file written: ${filePath}`);
    console.log("real HITL smoke passed");
  } catch (error) {
    const agentTail = await agent.tail();
    throw new Error(
      `${error.message}\n\nrelay tail:\n${relayTail.read()}\n\nagentunnel tail:\n${agentTail}`,
    );
  } finally {
    updates.close();
    await cleanupFile(filePath);
    await agent.stop();
    await stopProcess(relay);
  }
}

function resolveBinary(binary) {
  return path.isAbsolute(binary) ? binary : path.join(repoRoot, binary);
}

async function ensureExecutable(binary, name) {
  try {
    await fsp.access(binary, fs.constants.X_OK);
  } catch (error) {
    throw new Error(`${name} binary is not executable at ${binary}: ${error.message}`);
  }
}

function spawnProcess(command, args, extraEnv, tail) {
  const proc = spawn(command, args, {
    cwd: repoRoot,
    env: { ...process.env, ...extraEnv },
    detached: true,
    stdio: ["pipe", "pipe", "pipe"],
  });

  proc.stdout.on("data", (chunk) => tail.append(chunk));
  proc.stderr.on("data", (chunk) => tail.append(chunk));
  proc.on("error", (error) => tail.append(Buffer.from(`\nspawn error: ${error.message}\n`)));
  return { proc };
}

async function spawnPTYDriver(binary, args, extraEnv) {
  const proc = spawn("python3", [ptyDriver, binary, ...args], {
    cwd: repoRoot,
    env: { ...process.env, ...extraEnv },
    stdio: ["pipe", "pipe", "pipe"],
  });

  const lines = createLineReader(proc.stdout);
  const stderrTail = createTailBuffer();
  proc.stderr.on("data", (chunk) => stderrTail.append(chunk));

  const ready = await lines.next(10000);
  if (ready !== "READY") {
    throw new Error(`pty driver failed to start: ${ready}\n${stderrTail.read()}`);
  }

  return {
    async send(text) {
      proc.stdin.write(`send ${Buffer.from(text, "utf8").toString("base64")}\n`);
      const response = await lines.next(5000);
      if (response !== "OK") {
        throw new Error(`pty driver send failed: ${response}`);
      }
    },
    async tail() {
      proc.stdin.write("tail\n");
      const response = await lines.next(5000);
      if (!response.startsWith("TAIL ")) {
        return stderrTail.read();
      }
      return Buffer.from(response.slice(5), "base64").toString("utf8").trim() || "(empty)";
    },
    async stop() {
      if (proc.exitCode !== null) {
        return;
      }
      proc.stdin.write("stop\n");
      await waitForExit(proc, 5000);
    },
  };
}

async function stopProcess(processHandle) {
  if (!processHandle?.proc?.pid) {
    return;
  }
  try {
    process.kill(-processHandle.proc.pid, "SIGTERM");
  } catch {}

  const exited = await waitForExit(processHandle.proc, 5000);
  if (exited) {
    return;
  }

  try {
    process.kill(-processHandle.proc.pid, "SIGKILL");
  } catch {}
  await waitForExit(processHandle.proc, 5000);
}

function waitForExit(proc, timeoutMs) {
  return new Promise((resolve) => {
    let settled = false;

    const done = () => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      resolve(true);
    };

    const timer = setTimeout(() => {
      if (settled) {
        return;
      }
      settled = true;
      resolve(false);
    }, timeoutMs);

    proc.once("exit", done);
    proc.once("close", done);
  });
}

function createTailBuffer(maxBytes = 128 * 1024) {
  let data = "";
  return {
    append(chunk) {
      data += chunk.toString("utf8");
      if (data.length > maxBytes) {
        data = data.slice(-maxBytes);
      }
    },
    read() {
      return data.trim() || "(empty)";
    },
  };
}

function createLineReader(stream) {
  let buffer = "";
  const queue = [];
  const waiters = [];

  stream.on("data", (chunk) => {
    buffer += chunk.toString("utf8");
    while (true) {
      const newline = buffer.indexOf("\n");
      if (newline < 0) {
        break;
      }
      const line = buffer.slice(0, newline).replace(/\r$/, "");
      buffer = buffer.slice(newline + 1);
      if (waiters.length > 0) {
        waiters.shift()(line);
      } else {
        queue.push(line);
      }
    }
  });

  return {
    next(timeoutMs) {
      if (queue.length > 0) {
        return Promise.resolve(queue.shift());
      }
      return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error("timed out waiting for PTY driver output")), timeoutMs);
        waiters.push((line) => {
          clearTimeout(timeout);
          resolve(line);
        });
      });
    },
  };
}

function authHeader() {
  return {
    Authorization: "Basic " + Buffer.from(`${authUser}:${authPassword}`).toString("base64"),
  };
}

async function waitForHttp(url, timeoutMs, headers = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { headers });
      if (response.ok) {
        return;
      }
    } catch {}
    await sleep(200);
  }
  throw new Error(`timed out waiting for ${url}`);
}

async function findFreePort() {
  return await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(port);
      });
    });
    server.on("error", reject);
  });
}

function openEventSocket(url) {
  const ws = new WebSocket(url);
  const queue = [];
  const waiters = [];
  let closedError = null;

  ws.addEventListener("message", async (event) => {
    let data = event.data;
    if (data && typeof data.text === "function") {
      data = await data.text();
    }
    const parsed = JSON.parse(String(data));
    if (waiters.length > 0) {
      const waiter = waiters.shift();
      waiter.resolve(parsed);
      return;
    }
    queue.push(parsed);
  });

  ws.addEventListener("close", (event) => {
    closedError = new Error(`websocket closed: code=${event.code}`);
    while (waiters.length > 0) {
      waiters.shift().reject(closedError);
    }
  });

  ws.addEventListener("error", () => {
    if (!closedError) {
      closedError = new Error("websocket error");
    }
  });

  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timed out opening websocket ${url}`)), 10000);
    ws.addEventListener("open", () => {
      clearTimeout(timer);
      resolve({
        close() {
          ws.close();
        },
        async next(timeoutMs) {
          if (queue.length > 0) {
            return queue.shift();
          }
          if (closedError) {
            throw closedError;
          }
          return await new Promise((resolveNext, rejectNext) => {
            const waiter = { resolve: resolveNext, reject: rejectNext };
            waiters.push(waiter);
            const timeout = setTimeout(() => {
              const index = waiters.indexOf(waiter);
              if (index >= 0) {
                waiters.splice(index, 1);
              }
              rejectNext(new Error(`timed out waiting for websocket message from ${url}`));
            }, timeoutMs);
            waiter.resolve = (value) => {
              clearTimeout(timeout);
              resolveNext(value);
            };
            waiter.reject = (error) => {
              clearTimeout(timeout);
              rejectNext(error);
            };
          });
        },
        async waitFor(predicate, timeoutMs) {
          const deadline = Date.now() + timeoutMs;
          while (Date.now() < deadline) {
            const next = await this.next(deadline - Date.now());
            if (predicate(next)) {
              return next;
            }
          }
          throw new Error(`timed out waiting for matching websocket message from ${url}`);
        },
      });
    });
    ws.addEventListener("error", () => {
      clearTimeout(timer);
      reject(new Error(`failed to open websocket ${url}`));
    });
  });
}

async function waitForSessionInfo(relayPort, sessionId, predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const response = await fetch(`http://127.0.0.1:${relayPort}/api/sessions`, {
      headers: authHeader(),
    });
    if (!response.ok) {
      throw new Error(`GET /api/sessions returned ${response.status}`);
    }
    const sessions = await response.json();
    const session = sessions.find((value) => value.session_id === sessionId);
    if (session && predicate(session)) {
      return session;
    }
    await sleep(250);
  }
  throw new Error(`timed out waiting for session snapshot ${sessionId}`);
}

async function waitForFileContent(filePath, want, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const content = await fsp.readFile(filePath, "utf8");
      if (content.trim() === want) {
        return;
      }
    } catch {}
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${filePath} to contain ${want}`);
}

async function cleanupFile(filePath) {
  try {
    await fsp.unlink(filePath);
  } catch {}
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
