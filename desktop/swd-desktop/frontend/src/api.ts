// Typed wrappers around the generated Wails bindings.
// The bindings are JSDoc-annotated .js; TS infers types from them.
// @ts-ignore
import {
  WorkshopService,
  DownloadService,
  SubscriptionService,
  SettingsService,
} from "../bindings/github.com/xywf221/steam-workshop-downloader/desktop/services";

// Domain types (mirrors services/models).
export interface WorkshopItem {
  id: number;
  title: string;
  short_description: string;
  preview_url: string;
  file_size: number;
  file_size_human: string;
  subscriptions: number;
  favorited: number;
  views: number;
  time_created: number;
  time_updated: number;
  is_collection: boolean;
  consumer_appid: number;
  tags: string[];
  vote_score?: number;
  banned: boolean;
  steam_url: string;
}

export interface QueryResult {
  total: number;
  page: number;
  per_page: number;
  total_pages: number;
  next_cursor: string;
  items: WorkshopItem[];
}

export interface DownloadJob {
  id: number;
  app_id: number;
  workshop_id: number;
  title: string;
  status: string;
  files_total: number;
  files_done: number;
  bytes_done: number;
  bytes_human: string;
  percent: number;
  message: string;
  error?: string;
  out_dir: string;
}

export interface Subscription {
  workshop_id: number;
  app_id: number;
  title: string;
  manifest: number;
  remote_gid: number;
  has_update: boolean;
  never_sync: boolean;
  error?: string;
  synced?: string;
}

export interface SyncResult {
  workshop_id: number;
  title: string;
  skipped: boolean;
  files_ok: number;
  removed: string[];
  error?: string;
}

export interface Settings {
  proxy_url: string;
  output_dir: string;
}

export const api = {
  query: (appID: number, q: string, sort: string, page: number, perPage: number, cursor: string): Promise<QueryResult> =>
    WorkshopService.Query(appID, q, sort, page, perPage, cursor),
  getItem: (id: number): Promise<WorkshopItem> => WorkshopService.GetItem(id),

  enqueue: (appID: number, workshopID: number, title: string): Promise<DownloadJob[]> =>
    DownloadService.Enqueue(appID, workshopID, title),
  listDownloads: (): Promise<DownloadJob[]> => DownloadService.List(),
  clearFinished: (): Promise<number> => DownloadService.Clear(),

  listSubs: (): Promise<Subscription[]> => SubscriptionService.List(),
  addSub: (appID: number, workshopID: number): Promise<number> => SubscriptionService.Add(appID, workshopID),
  removeSub: (workshopID: number): Promise<void> => SubscriptionService.Remove(workshopID),
  updateSubs: (workshopID: number): Promise<SyncResult[]> => SubscriptionService.Update(workshopID),

  getSettings: (): Promise<Settings> => SettingsService.Get(),
  setSettings: (s: Settings): Promise<void> => SettingsService.Set(s),
  status: (): Promise<Record<string, any>> => SettingsService.Status(),
  testConnection: (): Promise<Record<string, any>> => SettingsService.TestConnection(),
};
