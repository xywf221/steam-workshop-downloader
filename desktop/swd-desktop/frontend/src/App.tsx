import { createContext, useCallback, useContext, useEffect, useState } from "react";
import Browse from "./views/Browse";
import Downloads from "./views/Downloads";
import Subscriptions from "./views/Subscriptions";
import Settings from "./views/Settings";

// ---------- tiny hash router ----------
type Route = { name: string; param?: string };

function parseHash(): Route {
  const h = window.location.hash.replace(/^#\/?/, "");
  const [name, param] = h.split("/");
  return { name: name || "browse", param };
}

function useRoute(): [Route, (r: Route) => void] {
  const [route, setRoute] = useState<Route>(parseHash);
  useEffect(() => {
    const onHash = () => setRoute(parseHash());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  const nav = useCallback((r: Route) => {
    window.location.hash = r.param ? `/${r.name}/${r.param}` : `/${r.name}`;
  }, []);
  return [route, nav];
}

// ---------- toast ----------
type Toast = { id: number; text: string; kind: "info" | "ok" | "err" };
const ToastCtx = createContext<(text: string, kind?: Toast["kind"]) => void>(() => {});

export function useToast() {
  return useContext(ToastCtx);
}

function ToastHost({ toasts }: { toasts: Toast[] }) {
  return (
    <div className="toast-root">
      {toasts.map((t) => (
        <div key={t.id} className={`toast ${t.kind === "info" ? "" : t.kind}`}>
          {t.text}
        </div>
      ))}
    </div>
  );
}

// ---------- app ----------
export default function App() {
  const [route, nav] = useRoute();
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [activeDownloads, setActiveDownloads] = useState(0);

  const toast = useCallback((text: string, kind: Toast["kind"] = "info") => {
    const id = Date.now() + Math.random();
    setToasts((ts) => [...ts, { id, text, kind }]);
    setTimeout(() => setToasts((ts) => ts.filter((t) => t.id !== id)), 3200);
  }, []);

  const tabs = [
    { name: "browse", label: "Browse" },
    { name: "subscriptions", label: "Subscriptions" },
    { name: "downloads", label: "Downloads", badge: activeDownloads },
    { name: "settings", label: "Settings" },
  ];

  return (
    <ToastCtx.Provider value={toast}>
      <div className="app">
        <header className="topbar">
          <div className="brand">
            <span className="brand-mark">↓</span>
            <span>SWD Workshop</span>
          </div>
          <nav className="nav">
            {tabs.map((t) => (
              <button
                key={t.name}
                className={route.name === t.name ? "active" : ""}
                onClick={() => nav({ name: t.name })}
              >
                {t.label}
                {t.badge ? <span className="badge">{t.badge}</span> : null}
              </button>
            ))}
          </nav>
        </header>
        <main className="main">
          {route.name === "browse" && <Browse nav={nav} />}
          {route.name === "item" && <Browse nav={nav} itemID={route.param} />}
          {route.name === "subscriptions" && <Subscriptions />}
          {route.name === "downloads" && <Downloads onActiveChange={setActiveDownloads} />}
          {route.name === "settings" && <Settings />}
        </main>
        <ToastHost toasts={toasts} />
      </div>
    </ToastCtx.Provider>
  );
}
