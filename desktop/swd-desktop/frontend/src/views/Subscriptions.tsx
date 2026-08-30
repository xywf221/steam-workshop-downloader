import React, { useEffect, useState } from "react";
import { api, Subscription } from "../api";
import { useToast } from "../App";

export default function Subscriptions() {
  const toast = useToast();
  const [subs, setSubs] = useState<Subscription[]>([]);
  const [appid, setAppid] = useState("");
  const [wid, setWid] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [loading, setLoading] = useState(true);

  const refresh = async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const list = await api.listSubs();
      setSubs(list || []);
      const pend = (list || []).filter((s) => s.has_update).length;
      setMsg(pend ? `${pend} update(s) pending` : "everything up to date");
    } catch (e: any) {
      setMsg(`check failed: ${e?.message ?? e}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { refresh(); }, []);

  const add = async (ev: React.FormEvent) => {
    ev.preventDefault();
    const a = parseInt(appid, 10);
    const w = parseInt(wid, 10);
    if (!a || !w) return;
    setBusy(true);
    try {
      const n = await api.addSub(a, w);
      toast(`Subscribed ${n} item(s)`, "ok");
      setAppid(""); setWid("");
      refresh(true);
    } catch (e: any) {
      toast(`Subscribe failed: ${e?.message ?? e}`, "err");
    } finally {
      setBusy(false);
    }
  };

  const update = async (id: number) => {
    setBusy(true);
    setMsg(id ? `updating ${id}…` : "updating all…");
    try {
      const results = await api.updateSubs(id);
      const updated = results.filter((r) => !r.skipped && !r.error).length;
      const failed = results.filter((r) => r.error).length;
      setMsg(`updated ${updated} · failed ${failed}`);
      if (failed) toast(`${failed} item(s) failed`, "err");
      else toast(`Updated ${updated} item(s)`, "ok");
      refresh(true);
    } catch (e: any) {
      setMsg(`update failed: ${e?.message ?? e}`);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (id: number) => {
    if (!confirm(`Unsubscribe ${id}? Downloaded files are kept.`)) return;
    try {
      await api.removeSub(id);
      setSubs((s) => s.filter((x) => x.workshop_id !== id));
      toast(`Unsubscribed ${id}`);
    } catch (e: any) {
      toast(`Remove failed: ${e?.message ?? e}`, "err");
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Subscriptions</h1>
        <p className="sub">Followed items · updates sync incrementally.</p>
      </div>

      <form className="sub-add-form panel" onSubmit={add}>
        <input className="input" value={appid} onChange={(e) => setAppid(e.target.value)} placeholder="AppID — e.g. 294100" inputMode="numeric" />
        <input className="input" value={wid} onChange={(e) => setWid(e.target.value)} placeholder="Workshop ID or collection ID" inputMode="numeric" />
        <button className="btn" type="submit" disabled={busy}>＋ Subscribe</button>
      </form>

      <div className="subs-toolbar">
        <button className="btn btn-ghost" onClick={() => refresh()} disabled={busy}>↻ Check updates</button>
        <button className="btn" onClick={() => update(0)} disabled={busy}>⬇ Update all</button>
        <span className="spacer" />
        <span className="subs-msg">{msg}</span>
      </div>

      <div className="sub-list">
        {loading && !subs.length && <p className="empty">Checking…</p>}
        {!loading && !subs.length && <p className="empty"><span className="empty-icon">📋</span>No subscriptions yet — add one above, or Subscribe on an item page.</p>}
        {subs.map((s) => (
          <article key={s.workshop_id} className="sub-card panel">
            <div>
              <div className="sub-title-row">
                <strong className="sub-title">{s.title}</strong>
                <Pill s={s} />
              </div>
              <div className="sub-meta">
                <span>app {s.app_id}</span>
                <span>id {s.workshop_id}</span>
                {s.synced && <span>synced {s.synced}</span>}
              </div>
            </div>
            <div className="sub-ops">
              <button className="btn btn-ghost btn-sm" onClick={() => update(s.workshop_id)} disabled={busy}>Update</button>
              <button className="btn btn-danger-ghost btn-sm" onClick={() => remove(s.workshop_id)}>Remove</button>
            </div>
          </article>
        ))}
      </div>
    </>
  );
}

function Pill({ s }: { s: Subscription }) {
  let cls = "st-ok";
  let text = "up to date";
  if (s.error) { cls = "st-err"; text = "unavailable"; }
  else if (s.never_sync) { cls = "st-new"; text = "new"; }
  else if (s.has_update) { cls = "st-upd"; text = "update available"; }
  return <span className={`pill ${cls}`}>{text}</span>;
}
