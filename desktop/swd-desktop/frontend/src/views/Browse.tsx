import React, { useEffect, useState } from "react";
import { api, WorkshopItem, QueryResult } from "../api";
import { useToast } from "../App";

type Nav = (r: { name: string; param?: string }) => void;

export default function Browse({ nav, itemID }: { nav: Nav; itemID?: string }) {
  if (itemID) {
    return <Detail id={parseInt(itemID, 10)} nav={nav} />;
  }
  return <SearchView nav={nav} />;
}

function SearchView({ nav }: { nav: Nav }) {
  const toast = useToast();
  const [appid, setAppid] = useState("294100");
  const [q, setQ] = useState("");
  const [sort, setSort] = useState("trend");
  const [result, setResult] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [page, setPage] = useState(1);
  const [cursor, setCursor] = useState("");

  const run = async (p: number, cur: string) => {
    const id = parseInt(appid, 10);
    if (!id) {
      setError("AppID must be a positive integer");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const res = await api.query(id, q, sort, p, 20, cur);
      setResult(res);
      setPage(p);
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setLoading(false);
    }
  };

  const submit = (ev: React.FormEvent) => {
    ev.preventDefault();
    setCursor("");
    run(1, "");
  };

  const enqueue = async (it: WorkshopItem, ev: React.MouseEvent) => {
    ev.stopPropagation();
    const id = parseInt(appid, 10) || it.consumer_appid;
    try {
      await api.enqueue(id, it.id, it.title);
      toast(`Queued: ${it.title}`, "ok");
    } catch (e: any) {
      toast(`Queue failed: ${e?.message ?? e}`, "err");
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Browse Workshop</h1>
        <p className="sub">Search and download items straight from Steam.</p>
      </div>

      <section className="search-panel panel">
        <form className="search-form" onSubmit={submit}>
          <label className="field">
            <span>AppID</span>
            <input className="input" value={appid} onChange={(e) => setAppid(e.target.value)} placeholder="294100" inputMode="numeric" />
          </label>
          <label className="field">
            <span>Search</span>
            <input className="input" value={q} onChange={(e) => setQ(e.target.value)} placeholder="mod name or keywords (optional)" />
          </label>
          <label className="field">
            <span>Sort by</span>
            <select className="input" value={sort} onChange={(e) => setSort(e.target.value)}>
              <option value="trend">Trending</option>
              <option value="votes">Top rated</option>
              <option value="subscriptions">Most subscribed</option>
              <option value="new">Newest</option>
              <option value="search">Text relevance</option>
            </select>
          </label>
          <button className="btn" type="submit" disabled={loading}>{loading ? "…" : "Search"}</button>
        </form>
        <p className="hint">RimWorld <code>294100</code> · Wallpaper Engine <code>431960</code></p>
      </section>

      {error && <div className="alert error">⚠ {error}</div>}

      {result && (
        <>
          <div className="results-meta">
            <span><strong>{result.total}</strong> results</span>
            <span className="dim">· page {result.page} of {result.total_pages}</span>
          </div>
          {result.items.length ? (
            <div className="card-grid">
              {result.items.map((it) => (
                <div key={it.id} className="card" onClick={() => nav({ name: "item", param: String(it.id) })}>
                  <div className="card-preview">
                    {it.preview_url ? <img src={it.preview_url} alt="" loading="lazy" referrerPolicy="no-referrer" /> : <div className="preview-placeholder">No preview</div>}
                    {it.is_collection && <span className="badge-tag">Collection</span>}
                  </div>
                  <div className="card-body">
                    <h2 className="card-title">{it.title}</h2>
                    <p className="card-desc">{it.short_description}</p>
                    <div className="card-stats">
                      <span>⬇ {it.subscriptions}</span>
                      <span>★ {it.favorited}</span>
                      {it.file_size_human && <span>{it.file_size_human}</span>}
                    </div>
                  </div>
                  <div className="card-actions">
                    <button className="btn btn-sm" onClick={(e) => enqueue(it, e)}>⬇ Download</button>
                    <button className="btn btn-ghost btn-sm" onClick={(e) => { e.stopPropagation(); nav({ name: "item", param: String(it.id) }); }}>View</button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="empty"><span className="empty-icon">🔍</span>No items on this page.</p>
          )}
          <nav className="pagination">
            <button className="btn btn-ghost" disabled={page <= 1 || loading} onClick={() => run(page - 1, cursor)}>← Prev</button>
            <span className="page-num">{result.page} / {result.total_pages}</span>
            <button className="btn btn-ghost" disabled={page >= result.total_pages || loading} onClick={() => { setCursor(result.next_cursor); run(page + 1, result.next_cursor); }}>Next →</button>
          </nav>
        </>
      )}
      {!result && !loading && <p className="empty"><span className="empty-icon">🎮</span>Enter an AppID and hit Search.</p>}
    </>
  );
}

function Detail({ id, nav }: { id: number; nav: Nav }) {
  const toast = useToast();
  const [item, setItem] = useState<WorkshopItem | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.getItem(id).then(setItem).catch((e) => setError(String(e?.message ?? e)));
  }, [id]);

  const enqueue = async () => {
    if (!item) return;
    setBusy(true);
    try {
      await api.enqueue(item.consumer_appid, item.id, item.title);
      toast(`Queued: ${item.title}`, "ok");
    } catch (e: any) {
      toast(`Queue failed: ${e?.message ?? e}`, "err");
    } finally {
      setBusy(false);
    }
  };

  const subscribe = async () => {
    if (!item) return;
    setBusy(true);
    try {
      const n = await api.addSub(item.consumer_appid, item.id);
      toast(`Subscribed ${n} item(s)`, "ok");
    } catch (e: any) {
      toast(`Subscribe failed: ${e?.message ?? e}`, "err");
    } finally {
      setBusy(false);
    }
  };

  if (error) return <div className="alert error">⚠ {error}</div>;
  if (!item) return <p className="empty">Loading…</p>;

  return (
    <>
      <p className="dim" style={{ marginBottom: "1rem" }}>
        <a href="#/browse" onClick={(e) => { e.preventDefault(); nav({ name: "browse" }); }}>← Back</a>
      </p>
      <article className="detail panel">
        <div>
          {item.preview_url ? <img className="hero" src={item.preview_url} alt="" referrerPolicy="no-referrer" /> : <div className="preview-placeholder large">No preview</div>}
        </div>
        <div>
          <h1>{item.title}</h1>
          <p className="meta">ID <code>{item.id}</code>{item.is_collection && <> · <span className="badge-tag" style={{ position: "static" }}>Collection</span></>}{item.banned && <> · <span style={{ color: "var(--danger)" }}>Banned</span></>}</p>
          <div className="stats-row">
            <span className="stat-chip">⬇ <b>{item.subscriptions}</b> subs</span>
            <span className="stat-chip">★ <b>{item.favorited}</b> favs</span>
            <span className="stat-chip">👁 <b>{item.views}</b> views</span>
            {item.file_size_human && <span className="stat-chip"><b>{item.file_size_human}</b></span>}
            {item.vote_score != null && <span className="stat-chip">score <b>{item.vote_score.toFixed(2)}</b></span>}
          </div>
          {!!item.tags.length && <div className="tags">{item.tags.map((t) => <span key={t} className="tag">{t}</span>)}</div>}
          {item.short_description && <div className="description">{item.short_description}</div>}
          <div className="actions">
            <button className="btn" onClick={enqueue} disabled={busy}>⬇ Download</button>
            <button className="btn btn-ghost" onClick={subscribe} disabled={busy}>＋ Subscribe</button>
            <a className="btn btn-ghost" href={item.steam_url} target="_blank" rel="noopener">Steam page ↗</a>
          </div>
        </div>
      </article>
    </>
  );
}
