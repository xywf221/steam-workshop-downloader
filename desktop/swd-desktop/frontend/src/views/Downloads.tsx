import { useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { api, DownloadJob } from "../api";
import { useToast } from "../App";

export default function Downloads({ onActiveChange }: { onActiveChange: (n: number) => void }) {
  const toast = useToast();
  const [jobs, setJobs] = useState<DownloadJob[]>([]);

  const refresh = async () => {
    try {
      const list = await api.listDownloads();
      setJobs(list || []);
      const active = (list || []).filter((j) => j.status === "queued" || j.status === "running").length;
      onActiveChange(active);
    } catch {
      /* transient */
    }
  };

  useEffect(() => {
    refresh();
    // Live progress events from the Go service.
    const off = Events.On("download:progress", () => { refresh(); });
    const t = setInterval(refresh, 2000); // fallback poll
    return () => { off(); clearInterval(t); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const counts = {
    queued: jobs.filter((j) => j.status === "queued").length,
    running: jobs.filter((j) => j.status === "running").length,
    done: jobs.filter((j) => j.status === "done").length,
    failed: jobs.filter((j) => j.status === "failed").length,
  };

  const clear = async () => {
    const n = await api.clearFinished();
    toast(`Cleared ${n} finished job(s)`);
    refresh();
  };

  return (
    <>
      <div className="page-head">
        <h1>Downloads</h1>
        <p className="sub">Local download queue · progress updates live.</p>
      </div>

      <div className="dl-summary">
        <span className="summary-chip c-queued"><span className="n">{counts.queued}</span><span className="l">queued</span></span>
        <span className="summary-chip c-running"><span className="n">{counts.running}</span><span className="l">running</span></span>
        <span className="summary-chip c-done"><span className="n">{counts.done}</span><span className="l">done</span></span>
        <span className="summary-chip c-failed"><span className="n">{counts.failed}</span><span className="l">failed</span></span>
        <span style={{ flex: 1 }} />
        <button className="btn btn-ghost btn-sm" onClick={clear}>Clear finished</button>
      </div>

      <div className="job-list">
        {!jobs.length && <p className="empty"><span className="empty-icon">📥</span>No downloads yet. Open an item and click Download.</p>}
        {jobs.map((j) => (
          <article key={j.id} className={`job-card panel status-${j.status}`}>
            <div className="job-top">
              <strong className="job-title">{j.title}</strong>
              <span className="job-status">{j.status}</span>
            </div>
            <div className="job-meta">
              <span>app {j.app_id}</span>
              <span>id {j.workshop_id}</span>
              <span>{j.out_dir}</span>
            </div>
            <div className="progress-bar"><div className="progress-fill" style={{ width: `${j.percent}%` }} /></div>
            <div className="job-stats">
              <span>{j.percent}%</span>
              <span>{j.files_done}/{j.files_total} files</span>
              <span>{j.bytes_human}</span>
              {j.message && <span>{j.message}</span>}
            </div>
            {j.error && <p className="job-error">{j.error}</p>}
          </article>
        ))}
      </div>
    </>
  );
}
