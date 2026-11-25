import type { NetworkAccess } from './resources'
import type { Resource } from './resources'
export type User = { id: string; alias: string; email: string; role: 'admin' | 'user'; disabled: boolean; max_devices: number; max_sessions: number; expires_at: string; resources: string[] }
export type Device = { id: string; user_id: string; client_type?: string; ip: string; created_at: string; delete_pending: boolean }
export type Lease = { id: string; user_id: string; device_id: string; ip: string; expires_at: string; resources: Resource[] }
export type AuditEntry = { sequence: number; at: string; actor: string; action: string; object: string }
export type AuditPage = { events: AuditEntry[]; next_before: number }
export type AdminState = { users: Record<string, User>; resources: Record<string, Resource>; devices: Record<string, Device>; leases: Record<string, Lease>; revision?: number; observed_revision?: number; last_error?: string; runtime?: { version: string; sampled_at: string; uptime_seconds: number; process_heap_bytes: number; authentication_failures_last_minute: number; alerts: {code:string;severity:string;message:string}[]; checks: {name:string;status:string;detail:string}[]; status?: string }; network?: NetworkAccess }
export const emptyState = (): AdminState => ({users:{},resources:{},devices:{},leases:{}})
