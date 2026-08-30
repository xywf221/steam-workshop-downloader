import React, { useEffect, useState } from "react";
import { api, Settings as SettingsT } from "../api";
import { useToast } from "../App";

export default function Settings() {
  const toast = useToast();
  const [form, setForm] = useState<SettingsT>({ proxy_url: "", output_dir: "" });
  const [status, setStatus] = useState<Record<string, any>>({});
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      const [s, st] = await Promise.all([api.getSettings(), api.status()]);
      setForm(s);
      setStatus(st || {});
    } catch (e: any) {
      toast(`Load failed: ${e?.message ?? e}`, "err");
    }
  };

  useEffect(() => { load(); }, []);

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault();
    setBusy(true);
    try {
      await api.setSettings(form);
      toast("Settings saved", "ok");
      load();
    } catch (e: any) {
      toast(`Save failed: ${e?.message ?? e}`, "err");
    } finally {
      setBusy(false);
    }
  };

  const test = async () => {
    setBusy(true);
    try {
      const st = await api.testConnection();
      setStatus(st || {});
      toast("Connected to Steam", "ok");
    } catch (e: any) {
      toast(`Connection failed: ${e?.message ?? e}`, "err");
    } finally {
      setBusy(false);
    }
  };

  const connected = !!status.connected;

  return (
    <>
      <div className="page-head">
        <h1>Settings</h1>
        <p className="sub">Connection, proxy, and download directory.</p>
      </div>

      <div className="status-grid">
        <div className="status-cell panel">
          <div className="k">Steam</div>
          <div className={`v ${connected ? "ok" : "err"}`}>{connected ? "Connected" : "Disconnected"}</div>
        </div>
        <div className="status-cell panel">
          <div className="k">Mode</div>
          <div className="v">{status.direct ? "Direct (stealth)" : "Proxy"}</div>
        </div>
        <div className="status-cell panel">
          <div className="k">Proxy</div>
          <div className="v" style={{ fontSize: "0.85rem", wordBreak: "break-all" }}>{status.proxy || "—"}</div>
        </div>
      </div>

      <form className="settings-form" onSubmit={save}>
        <label className="field">
          <span>Proxy URL (empty = direct with stealth TLS)</span>
          <input
            className="input"
            value={form.proxy_url}
            onChange={(e) => setForm({ ...form, proxy_url: e.target.value })}
            placeholder="socks5h://127.0.0.1:1080 or http://127.0.0.1:8080"
          />
          <span className="hint">Leave empty for direct connection. Mainland-China direct uses no-SNI stealth TLS automatically.</span>
        </label>
        <label className="field">
          <span>Download directory</span>
          <input
            className="input"
            value={form.output_dir}
            onChange={(e) => setForm({ ...form, output_dir: e.target.value })}
            placeholder="C:\path\to\downloads"
          />
          <span className="hint">Where workshop files and subscriptions.json are stored.</span>
        </label>
        <div className="actions">
          <button className="btn" type="submit" disabled={busy}>Save</button>
          <button className="btn btn-ghost" type="button" onClick={test} disabled={busy}>Test connection</button>
        </div>
      </form>
    </>
  );
}
