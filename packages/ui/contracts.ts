import type { AdminPageState, AdminSummary } from './models.generated'
export type { User, Device, Lease, DeleteResult, CleanupRetry } from './models.generated'
export type AdminRequest = <T = unknown>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>
export type { AuditEntry, AuditPage } from './models.generated'
// The view keeps empty collections while either generated response is loading.
export type AdminState = Partial<AdminPageState & AdminSummary> & Pick<AdminPageState, 'users' | 'resources' | 'devices' | 'leases'>
export const emptyState = (): AdminState => ({users:{},resources:{},devices:{},leases:{}})

export type { HostSummary as RemoteHost, GrantSummary as RemoteGrant, RemoteSession, InventoryPage as RemoteInventory, UserChoice, UserChoices, RemoteHistory as RemoteHistoryPage } from './models.generated'
