import type { NetworkAccess } from './resources'
import type { Resource } from './resources'
export type User = { id: string; alias: string; name: string; email: string; role: 'admin' | 'user'; disabled: boolean; delete_pending?: boolean; max_devices: number; max_sessions: number; expires_at: string; resources: string[] }
export type Device = { id: string; user_id: string; client_type?: string; ip: string; created_at: string; delete_pending: boolean }
export type Lease = { id: string; user_id: string; device_id: string; ip: string; expires_at: string; resources: Resource[] }
export type AuditEntry = { sequence: number; at: string; actor: string; action: string; object: string }
export type AuditPage = { events: AuditEntry[]; next_before: number }
export type AdminState = { counts?: Record<string, number>; next_after?: string; users: Record<string, User>; resources: Record<string, Resource>; devices: Record<string, Device>; leases: Record<string, Lease>; revision?: number; observed_revision?: number; last_error?: string; runtime?: { version: string; sampled_at: string; uptime_seconds: number; process_heap_bytes: number; process_goroutines: number; requests: number; request_errors: number; host_resources: { process_cpu_seconds: number; process_max_rss_bytes: number; database_disk_available_bytes: number; database_disk_total_bytes: number }; authentication_failures_last_minute: number; alerts: {code:string;severity:string;message:string}[]; checks: {name:string;status:string;detail:string}[]; status?: string }; network?: NetworkAccess }
export const emptyState = (): AdminState => ({users:{},resources:{},devices:{},leases:{}})

export type RemoteHost = { id: string; name: string; platform: string; status: string; require_consent: boolean; capabilities: number }
export type RemoteGrant = { id: string; user_id: string; host_id: string; capabilities: number; active: boolean; expires_at: string }
export type RemoteSession = { id: string; user_id: string; host_id: string; state: string; capabilities: number; created_at: string; closed_at: string }
export type RemoteInventory = { hosts: RemoteHost[]; grants: RemoteGrant[]; sessions: RemoteSession[]; users: Record<string, string>; next_after: string }
export type UserChoice = { id: string; alias: string; name: string }
export type UserChoices = { users: UserChoice[]; next_after: string }
export type RemoteHistoryPage = { sessions: RemoteSession[]; next_before: string; users: Record<string, string>; hosts: Record<string, string> }
