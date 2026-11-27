import type { NetworkAccess } from './resources'
import type { Resource } from './resources'
import type { User, Device, Lease, RuntimeReport } from './models.generated'
export type { User, Device, Lease, DeleteResult, CleanupRetry } from './models.generated'
export type AdminRequest = <T = unknown>(path: string, method?: string, body?: unknown) => Promise<T>
export type AuditEntry = { sequence: number; at: string; actor: string; action: string; object: string }
export type AuditPage = { events: AuditEntry[]; next_before: number }
export type AdminState = { counts?: Record<string, number>; next_after?: string; users: Record<string, User>; resources: Record<string, Resource>; devices: Record<string, Device>; leases: Record<string, Lease>; revision?: number; observed_revision?: number; last_error?: string; runtime?: RuntimeReport; network?: NetworkAccess }
export const emptyState = (): AdminState => ({users:{},resources:{},devices:{},leases:{}})

export type { HostSummary as RemoteHost, GrantSummary as RemoteGrant, RemoteSession, InventoryPage as RemoteInventory, UserChoice, UserChoices, RemoteHistory as RemoteHistoryPage } from './models.generated'
