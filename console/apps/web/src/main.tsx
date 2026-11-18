import * as React from 'react';
import { createRoot } from 'react-dom/client';
import type { ClusterResource, Condition, NodePoolResource, OidcConfigDetail, OidcConfigResource, ResourceList } from '@seaplane/platform-api-contracts';
import { api, listLinkableArns, setSelectedArn, getSelectedArn, type LinkableArn } from './api';
import * as oidc from './auth';
import type { ConsoleUser } from './auth';
import './style.css';

interface AuthResponse {
  authenticated: boolean;
  user?: ConsoleUser | undefined;
}

interface LinkState {
  arns: LinkableArn[];
  selected?: string | undefined;
}

function App() {
  const [auth, setAuth] = React.useState<AuthResponse>();
  const [authError, setAuthError] = React.useState(false);
  const [links, setLinks] = React.useState<LinkState>({ arns: [] });
  const [clusters, setClusters] = React.useState<ClusterResource[]>([]);
  const [clusterOffset, setClusterOffset] = React.useState(0);
  const [hasMoreClusters, setHasMoreClusters] = React.useState(false);
  const [selectedCluster, setSelectedCluster] = React.useState<ClusterResource>();
  const [nodePools, setNodePools] = React.useState<NodePoolResource[]>([]);
  const [oidcConfig, setOidcConfig] = React.useState<OidcConfigDetail>();
  const [selectedClusters, setSelectedClusters] = React.useState<Set<string>>(new Set());
  const [confirmingDelete, setConfirmingDelete] = React.useState(false);
  const [deleting, setDeleting] = React.useState(false);
  const [deleteResults, setDeleteResults] = React.useState<Array<{ name: string; ok: boolean; message: string }>>();
  const [oidcConfigs, setOidcConfigs] = React.useState<OidcConfigResource[]>();
  const [oidcConfigsError, setOidcConfigsError] = React.useState<string>();
  const [confirmingOidcCreate, setConfirmingOidcCreate] = React.useState(false);
  const [creatingOidc, setCreatingOidc] = React.useState(false);
  const [deletingOidc, setDeletingOidc] = React.useState<string>();
  const [confirmingOidcDelete, setConfirmingOidcDelete] = React.useState<OidcConfigResource>();
  const [nodePoolOffset, setNodePoolOffset] = React.useState(0);
  const [hasMoreNodePools, setHasMoreNodePools] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string>();

  const loadClusters = React.useCallback(async (offset: number) => {
    setBusy(true);
    setError(undefined);
    try {
      const result = await api<ResourceList<ClusterResource>>(`/bff/v1/clusters?limit=50&offset=${offset}`);
      setClusters(result.items ?? []);
      setClusterOffset(offset);
      setSelectedClusters(new Set());
      setHasMoreClusters((result.items?.length ?? 0) === 50);
      setSelectedCluster(undefined);
      setNodePools([]);
    } catch (cause) {
      setError(messageFor(cause));
    } finally {
      setBusy(false);
    }
  }, []);

  const loadNodePools = React.useCallback(async (clusterId: string, offset: number) => {
    setBusy(true);
    setError(undefined);
    try {
      const result = await api<ResourceList<NodePoolResource>>(
        `/bff/v1/clusters/${encodeURIComponent(clusterId)}/nodepools?limit=50&offset=${offset}`,
      );
      setNodePools(result.items ?? []);
      setNodePoolOffset(offset);
      setHasMoreNodePools((result.items?.length ?? 0) === 50);
    } catch (cause) {
      setError(messageFor(cause));
    } finally {
      setBusy(false);
    }
  }, []);

  React.useEffect(() => {
    void (async () => {
      try {
        const completed = await oidc.completeLogin();
        if (completed) {
          setAuthError(false);
        } else if (new URLSearchParams(window.location.search).has('authError')) {
          setAuthError(true);
          window.history.replaceState({}, '', window.location.pathname);
        }
      } catch (cause) {
        setAuthError(true);
        setError(messageFor(cause));
      }
      if (!oidc.isSignedIn()) {
        setAuth({ authenticated: false });
        return;
      }
      setAuth({ authenticated: true, user: oidc.currentUser() });
      try {
        // Flow 2a: the ARNs this user may operate as, for the selector.
        const arns = await listLinkableArns();
        const preferred = getSelectedArn();
        const selected = arns.some((l) => l.arn === preferred) ? preferred : arns[0]?.arn;
        setSelectedArn(selected);
        setLinks({ arns, selected });
        if (selected) {
          void loadOidcConfigs();
          void loadClusters(0);
        }
      } catch (cause) {
        setError(messageFor(cause));
      }
    })();
  }, [loadClusters]);

  async function selectCluster(cluster: ClusterResource) {
    setSelectedCluster(cluster);
    setNodePools([]);
    setNodePoolOffset(0);
    const name = cluster.metadata.name;
    try {
      const detail = await api<ClusterResource>(`/bff/v1/clusters/${encodeURIComponent(name)}`);
      setSelectedCluster(detail);
    } catch (cause) {
      setError(messageFor(cause));
    }
    // The Platform API's nodepool list filters by the parent Cluster UID
    // (UUID), not the cluster name — metadata.uid is required here.
    await loadNodePools(cluster.metadata.uid || name, 0);
    // Fetch the cluster's OIDC config for the identity section.
    if (cluster.spec.oidcConfigId) void loadOidcConfig(cluster.spec.oidcConfigId);
  }

  async function loadOidcConfig(configId: string) {
    setOidcConfig(undefined);
    try {
      setOidcConfig(await api<OidcConfigDetail>(`/bff/v1/oidcconfigs/${encodeURIComponent(configId)}`));
    } catch {
      // A denied or missing OIDC config is not fatal for the detail view;
      // the section renders placeholders.
      setOidcConfig(undefined);
    }
  }

  async function loadOidcConfigs() {
    setOidcConfigsError(undefined);
    try {
      const result = await api<ResourceList<OidcConfigResource>>('/bff/v1/oidcconfigs?limit=50&offset=0');
      setOidcConfigs(result.items ?? []);
    } catch (cause) {
      setOidcConfigs(undefined);
      setOidcConfigsError(messageFor(cause));
    }
  }

  async function createOidcConfig() {
    setConfirmingOidcCreate(false);
    setCreatingOidc(true);
    try {
      await api<void>('/bff/v1/oidcconfigs', {
        method: 'POST',
        headers: { 'x-console-request': '1' },
        body: JSON.stringify({ type: 'managed' }),
      });
      await loadOidcConfigs();
    } catch (cause) {
      setError(messageFor(cause));
    } finally {
      setCreatingOidc(false);
    }
  }

  async function deleteOidcConfig(name: string) {
    const target = confirmingOidcDelete;
    setConfirmingOidcDelete(undefined);
    setDeletingOidc(name);
    try {
      await api<{ message?: string }>(`/bff/v1/oidcconfigs/${encodeURIComponent(name)}`, {
        method: 'DELETE',
        headers: { 'x-console-request': '1' },
      });
      await loadOidcConfigs();
    } catch (cause) {
      setError(messageFor(cause));
    } finally {
      setDeletingOidc(undefined);
      void target; // captured only to close over the confirmed row
    }
  }

  function toggleCluster(name: string, checked: boolean) {
    setSelectedClusters((previous) => {
      const next = new Set(previous);
      if (checked) next.add(name);
      else next.delete(name);
      return next;
    });
  }

  async function deleteSelectedClusters() {
    setConfirmingDelete(false);
    setDeleting(true);
    setDeleteResults(undefined);
    const results: Array<{ name: string; ok: boolean; message: string }> = [];
    // Per-cluster calls: partial success is possible and each outcome is
    // attributable (Cedar denial, delete protection, already gone).
    for (const name of selectedClusters) {
      try {
        const result = await api<{ message?: string }>(`/bff/v1/clusters/${encodeURIComponent(name)}`, {
          method: 'DELETE',
          headers: { 'x-console-request': '1' },
        });
        results.push({ name, ok: true, message: result?.message || 'Deletion initiated' });
      } catch (cause) {
        results.push({ name, ok: false, message: messageFor(cause) });
      }
    }
    setDeleteResults(results);
    setSelectedClusters(new Set());
    setDeleting(false);
    void loadClusters(clusterOffset);
  }

  async function signOut() {
    // RP-initiated logout through the OIDC client: ends the Keycloak SSO
    // session and navigates home, resetting all client state.
    try {
      await oidc.logout();
    } catch (cause) {
      setError(messageFor(cause));
    }
  }

  if (!auth) {
    return <main className="page"><div className="loading-card">Connecting to the console…</div></main>;
  }

  if (!auth.authenticated) {
    return (
      <main className="page auth-page">
        <div className="brand-mark" aria-hidden="true">S</div>
        <p className="eyebrow">SEAPLANE PLATFORM</p>
        <h1>Cluster console</h1>
        <p className="muted">Sign in to view the clusters and node pools available to your AWS role.</p>
        {authError && <p className="notice error" role="alert">Sign-in did not complete. Please try again.</p>}
        {error && <p className="notice error" role="alert">{error}</p>}
        <button className="button primary" onClick={() => void oidc.login().catch((cause: unknown) => setError(messageFor(cause)))}>Sign in</button>
      </main>
    );
  }

  return (
    <main className="page">
      <header className="topbar">
        <div className="brand"><span className="brand-mark small">S</span><span>Seaplane <b>Console</b></span></div>
        <div className="account">
          <div className="user-meta">
            <span className="user-label">{auth.user?.name || auth.user?.email || 'Signed in'}</span>
            {links.arns.length > 0 && (
              <select
                className="user-sub mono"
                title="Operate as (linked ARN)"
                value={links.selected ?? ''}
                disabled={busy}
                onChange={(event) => {
                  const arn = event.target.value || undefined;
                  setSelectedArn(arn);
                  setLinks((current) => ({ ...current, selected: arn }));
                  if (arn) {
                    void loadOidcConfigs();
                    void loadClusters(0);
                  }
                }}
              >
                {links.arns.map((link) => (
                  <option key={link.arn} value={link.arn}>{shortenArn(link.arn)}</option>
                ))}
              </select>
            )}
          </div>
          <button className="button quiet" onClick={signOut} disabled={busy}>Sign out</button>
        </div>
      </header>

      <section className="intro">
        <p className="eyebrow">YOUR INFRASTRUCTURE</p>
        <h1>Clusters</h1>
        <p className="muted">Browse cluster status and the node pools associated with each cluster.</p>
      </section>

      {error && <div className="notice error" role="alert">{error}</div>}

      <section className="panel oidc-panel" aria-labelledby="oidc-heading">
        <div className="panel-heading">
          <div><h2 id="oidc-heading">OIDC configs</h2><span className="muted small-text">{oidcConfigs?.length ?? 0} shown</span></div>
          <div className="oidc-actions">
            <button className="button quiet" onClick={() => void loadOidcConfigs()} disabled={creatingOidc || busy}>Refresh</button>
            <button className="button" onClick={() => setConfirmingOidcCreate(true)} disabled={creatingOidc}>
              New managed config
            </button>
          </div>
        </div>
        {oidcConfigsError && <div className="empty compact">{oidcConfigsError}</div>}
        {!oidcConfigsError && oidcConfigs?.length === 0 && !busy && <div className="empty compact">No OIDC configs found.</div>}
        <div className="oidc-list">
          {oidcConfigs?.map((config) => (
            <div className="oidc-row" key={config.metadata.name}>
              <div className="oidc-main">
                <strong title={config.metadata.name}>{config.metadata.name}</strong>
                <span className="muted small-text" title={config.spec.issuerUrl}>
                  {config.spec.issuerUrl || 'issuer pending'}
                </span>
              </div>
              <span className={`type-badge ${config.spec.type === 'managed' ? 'managed' : 'unmanaged'}`}>
                {config.spec.type}
              </span>
              <StatusBadge conditions={config.status?.conditions} phase={config.status?.phase === 'Ready' ? 'Ready' : config.status?.phase} />
              <span className="muted small-text">
                {config.status?.lastUsedTimestamp ? `Used ${formatDate(config.status.lastUsedTimestamp)}` : 'Unused'}
              </span>
              {!config.status?.lastUsedTimestamp && (
                <button
                  className="button quiet small-text"
                  onClick={() => setConfirmingOidcDelete(config)}
                  disabled={deletingOidc !== undefined}
                  title="Delete this unused OIDC config"
                >
                  {deletingOidc === config.metadata.name ? 'Deleting…' : 'Delete'}
                </button>
              )}
            </div>
          ))}
        </div>
        {confirmingOidcDelete && (
          <div className="modal-overlay" role="dialog" aria-modal="true" aria-label="Confirm OIDC config deletion">
            <div className="modal">
              <h3>Delete OIDC config?</h3>
              <p className="muted">
                This initiates deletion of the unused config
                <b> {confirmingOidcDelete.metadata.name}</b>. The action is irreversible.
                {confirmingOidcDelete.status?.phase !== 'Ready'
                  ? ' Note: the config is still pending — the platform may reject deletion until provisioning completes.'
                  : ''}
              </p>
              <div className="modal-actions">
                <button className="button quiet" onClick={() => setConfirmingOidcDelete(undefined)} disabled={deletingOidc !== undefined}>Cancel</button>
                <button className="button danger" onClick={() => void deleteOidcConfig(confirmingOidcDelete.metadata.name)} disabled={deletingOidc !== undefined}>
                  {deletingOidc ? 'Deleting…' : 'Delete'}
                </button>
              </div>
            </div>
          </div>
        )}
        {confirmingOidcCreate && (
          <div className="modal-overlay" role="dialog" aria-modal="true" aria-label="Confirm OIDC config creation">
            <div className="modal">
              <h3>Create a managed OIDC config?</h3>
              <p className="muted">
                The platform will provision a new OIDC issuer for this account. The config appears as
                pending until the issuer is ready.
              </p>
              <div className="modal-actions">
                <button className="button quiet" onClick={() => setConfirmingOidcCreate(false)} disabled={creatingOidc}>Cancel</button>
                <button className="button" onClick={() => void createOidcConfig()} disabled={creatingOidc}>
                  {creatingOidc ? 'Creating…' : 'Create'}
                </button>
              </div>
            </div>
          </div>
        )}
      </section>

      <div className="workspace">
        <section className="panel cluster-panel" aria-labelledby="cluster-heading">
          <div className="panel-heading">
            <div><h2 id="cluster-heading">All clusters</h2><span className="muted small-text">{clusters.length} shown</span></div>
            <button className="button quiet" onClick={() => void loadClusters(clusterOffset)} disabled={busy}>Refresh</button>
          </div>
          {selectedClusters.size > 0 && (
            <div className="delete-bar" role="region" aria-label="Cluster deletion">
              <span>
                <b>{selectedClusters.size}</b> selected: {[...selectedClusters].join(', ')}
              </span>
              <div className="delete-bar-actions">
                <button className="button quiet" onClick={() => setSelectedClusters(new Set())} disabled={deleting}>Clear</button>
                <button className="button danger" onClick={() => setConfirmingDelete(true)} disabled={deleting}>
                  Delete{selectedClusters.size > 1 ? ` ${selectedClusters.size} clusters` : ' cluster'}
                </button>
              </div>
            </div>
          )}
          {deleteResults && (
            <div className="delete-results" role="status">
              {deleteResults.map((result) => (
                <div key={result.name} className={`delete-result ${result.ok ? 'ok' : 'failed'}`}>
                  <strong>{result.name}</strong>
                  <span>{result.ok ? '✓' : '✗'} {result.message}</span>
                </div>
              ))}
            </div>
          )}
          {confirmingDelete && (
            <div className="modal-overlay" role="dialog" aria-modal="true" aria-label="Confirm cluster deletion">
              <div className="modal">
                <h3>Delete {selectedClusters.size > 1 ? `${selectedClusters.size} clusters` : 'cluster'}?</h3>
                <p className="muted">This initiates deletion of the following clusters. The action is irreversible.</p>
                <ul className="modal-list">
                  {clusters
                    .filter((cluster) => selectedClusters.has(cluster.metadata.name))
                    .map((cluster) => (
                      <li key={cluster.metadata.name}>
                        <strong>{cluster.metadata.name}</strong>
                        {cluster.spec.deleteProtection
                          ? <span className="notice error inline"> delete protection enabled — the platform may reject this</span>
                          : null}
                      </li>
                    ))}
                </ul>
                <div className="modal-actions">
                  <button className="button quiet" onClick={() => setConfirmingDelete(false)} disabled={deleting}>Cancel</button>
                  <button className="button danger" onClick={() => void deleteSelectedClusters()} disabled={deleting}>
                    {deleting ? 'Deleting…' : 'Delete'}
                  </button>
                </div>
              </div>
            </div>
          )}
          {busy && clusters.length === 0 ? <div className="empty">Loading clusters…</div> : null}
          {!busy && clusters.length === 0 && !error ? <div className="empty">No clusters found for this role.</div> : null}
          <div className="cluster-list">
            {clusters.map((cluster) => {
              const name = cluster.metadata.name;
              const selected = selectedCluster?.metadata.name === name;
              return (
                <button className={`cluster-row ${selected ? 'selected' : ''}`} key={cluster.metadata.uid || name}
                  onClick={() => void selectCluster(cluster)} aria-pressed={selected}>
                  <input
                    type="checkbox"
                    className="cluster-checkbox"
                    aria-label={`Select ${name} for deletion`}
                    checked={selectedClusters.has(name)}
                    onClick={(event) => event.stopPropagation()}
                    onChange={(event) => toggleCluster(name, event.target.checked)}
                    disabled={deleting}
                  />
                  <span className="cluster-icon" aria-hidden="true">⌘</span>
                  <span className="cluster-copy"><strong>{name}</strong><span>{cluster.metadata.creationTimestamp ? `Created ${formatDate(cluster.metadata.creationTimestamp)}` : 'Cluster resource'}</span></span>
                  <StatusBadge conditions={cluster.status?.conditions} phase={cluster.status?.phase} />
                  <span className="chevron" aria-hidden="true">›</span>
                </button>
              );
            })}
          </div>
          <Pagination offset={clusterOffset} count={clusters.length} hasMore={hasMoreClusters} busy={busy}
            onChange={(next) => void loadClusters(next)} />

        </section>

        <section className="panel detail-panel" aria-labelledby="detail-heading">
          {!selectedCluster ? (
            <div className="detail-empty"><span className="detail-symbol" aria-hidden="true">↖</span><h2 id="detail-heading">Choose a cluster</h2><p className="muted">Select a cluster to see its status and node pools.</p></div>
          ) : (
            <>
              <div className="detail-title">
                <div><p className="eyebrow">CLUSTER</p><h2 id="detail-heading">{selectedCluster.metadata.name}</h2></div>
                <StatusBadge conditions={selectedCluster.status?.conditions} phase={selectedCluster.status?.phase} />
              </div>
              <SectionTitle>Overview</SectionTitle>
              <div className="facts">
                <Fact label="Lifecycle phase" value={formatPhase(selectedCluster.status?.phase)} />
                <Fact label="OpenShift version" value={selectedCluster.status?.version || '—'} />
                <Fact label="Region" value={clusterAws(selectedCluster)?.region || '—'} />
                <Fact label="Created" value={selectedCluster.metadata.creationTimestamp ? formatDate(selectedCluster.metadata.creationTimestamp) : '—'} />
              </div>
              <div className="facts">
                <Fact label="API URL" value={apiUrl(selectedCluster)} />
                <Fact label="DNS" value={selectedCluster.status?.baseDomain || '—'} />
                <Fact label="Management cluster" value={selectedCluster.status?.placementRef?.managementCluster || '—'} />
                <Fact label="Resource ID" value={selectedCluster.metadata.uid || selectedCluster.metadata.name} />
              </div>
              <div className="facts">
                <Fact label="Endpoint access" value={endpointAccess(selectedCluster)} />
                <Fact label="FIPS mode" value={selectedCluster.spec.hostedCluster?.fips === true ? 'Enabled' : selectedCluster.spec.hostedCluster?.fips === false ? 'Disabled' : '—'} />
                <Fact label="Multi-arch" value={clusterAws(selectedCluster)?.multiArch === undefined ? '—' : clusterAws(selectedCluster)?.multiArch ? 'Enabled' : 'Disabled'} />
                <Fact label="Delete protection" value={selectedCluster.spec.deleteProtection === undefined ? '—' : selectedCluster.spec.deleteProtection ? 'Enabled' : 'Disabled'} />
              </div>
              <SectionTitle>Networking</SectionTitle>
              <div className="facts">
                <Fact label="Machine CIDR" value={cidrList(selectedCluster.spec.hostedCluster?.networking?.machineNetwork)} />
                <Fact label="Pod CIDR" value={cidrList(selectedCluster.spec.hostedCluster?.networking?.clusterNetwork)} />
                <Fact label="Service CIDR" value={cidrList(selectedCluster.spec.hostedCluster?.networking?.serviceNetwork)} />
                <Fact label="Host prefix" value={hostPrefix(selectedCluster)} />
              </div>
              <div className="facts">
                <Fact label="VPC" value={clusterAws(selectedCluster)?.cloudProviderConfig?.vpc || '—'} />
                <Fact label="Subnet" value={clusterAws(selectedCluster)?.cloudProviderConfig?.subnet?.id || '—'} />
                <Fact label="Zone" value={clusterAws(selectedCluster)?.cloudProviderConfig?.zone || '—'} />
              </div>
              <SectionTitle>Identity and access</SectionTitle>
              <div className="facts">
                <Fact label="OIDC endpoint" value={selectedCluster.spec.hostedCluster?.issuerURL || '—'} />
                <Fact label="OIDC config" value={selectedCluster.spec.oidcConfigId || '—'} />
                <Fact label="OIDC type" value={oidcConfig ? `${oidcConfig.spec.type === 'managed' ? 'Managed' : oidcConfig.spec.type === 'unmanaged' ? 'Unmanaged' : oidcConfig.spec.type}` : '—'} />
                <Fact label="OIDC status" value={oidcConfig ? formatPhase(oidcConfig.status?.phase) : '—'} />
              </div>
              <div className="facts">
                <Fact label="DNS reservation" value={selectedCluster.spec.dnsReservationId || '—'} />
                <Fact label="Installer role" value={oidcConfig?.spec.installerRoleArn || '—'} />
                <Fact label="Last used" value={oidcConfig?.status?.lastUsedTimestamp ? formatDate(oidcConfig.status.lastUsedTimestamp) : '—'} />
              </div>
              <OperatorRoles roles={clusterAws(selectedCluster)?.rolesRef} />
              <ConditionList conditions={selectedCluster.status?.conditions} />
              <div className="pool-heading"><div><h3>Node pools</h3><p className="muted small-text">Pools belonging to this cluster</p></div></div>
              {nodePools.length === 0 && !busy ? <div className="empty compact">No node pools found.</div> : null}
              <div className="pool-list">
                {nodePools.map((pool) => <NodePoolCard key={pool.metadata.uid || pool.metadata.name} pool={pool} />)}
              </div>
              <Pagination offset={nodePoolOffset} count={nodePools.length} hasMore={hasMoreNodePools} busy={busy}
                onChange={(next) => void loadNodePools(selectedCluster.metadata.uid || selectedCluster.metadata.name, next)} compact />
            </>
          )}
        </section>
      </div>

      <footer>Read-only preview <span aria-hidden="true">·</span> Access is governed by your AWS role and platform policies.</footer>
    </main>
  );
}

function messageFor(error: unknown): string {
  return error instanceof Error ? error.message : 'The request could not be completed.';
}

function shortenArn(arn: string): string {
  // arn:aws:sts::<account>:assumed-role/<role>/<session> -> ****<last4>/<role>/<session>
  // The account ID is partially masked in the topbar; the full ARN stays
  // available in the title attribute.
  const parts = arn.split(':');
  const account = parts[4] || '';
  const maskedAccount = account ? `****${account.slice(-4)}` : '';
  const tail = parts.slice(5).join(':').replace(/^assumed-role\//, '');
  const shown = [maskedAccount, tail].filter(Boolean).join('/');
  return shown.length > 48 ? `${shown.slice(0, 45)}…` : shown;
}

function clusterAws(cluster: ClusterResource) {
  return cluster.spec.hostedCluster?.platform?.aws;
}

function apiUrl(cluster: ClusterResource): string {
  const endpoint = cluster.status?.controlPlaneEndpoint;
  if (!endpoint?.host) return '—';
  const port = endpoint.port && endpoint.port !== 443 ? `:${endpoint.port}` : '';
  return `https://${endpoint.host}${port}`;
}

function endpointAccess(cluster: ClusterResource): string {
  const access = clusterAws(cluster)?.endpointAccess;
  if (access === undefined) return '—';
  return access === 'Private' ? 'Private' : access === 'PublicAndPrivate' ? 'Public and private' : 'Public';
}

function cidrList(network?: Array<{ cidr?: string }>): string {
  return network?.length ? network.map((entry) => entry.cidr).filter(Boolean).join(', ') || '—' : '—';
}

function hostPrefix(cluster: ClusterResource): string {
  const entry = cluster.spec.hostedCluster?.networking?.clusterNetwork?.[0];
  return entry?.hostPrefix !== undefined ? `/${entry.hostPrefix}` : '—';
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return <div className="section-title"><h3>{children}</h3></div>;
}

function OperatorRoles({ roles }: { roles?: Record<string, string> | undefined }) {
  if (!roles || Object.keys(roles).length === 0) return null;
  return (
    <div className="operator-roles">
      <div className="section-title"><h3>Operator IAM roles</h3></div>
      <ul>
        {Object.entries(roles).map(([key, arn]) => (
          <li key={key} title={arn}>
            <span className="muted small-text">{key.replace(/ARN$/, '')}</span>
            <code>{arn}</code>
          </li>
        ))}
      </ul>
    </div>
  );
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(date);
}

function StatusBadge({ conditions, phase }: { conditions?: Condition[] | undefined; phase?: string | undefined }) {
  const degraded = conditions?.some((condition) => condition.type === 'Degraded' && condition.status === 'True');
  const readiness = conditions?.find((condition) => condition.type === 'Ready' || condition.type === 'Available');
  const ready = !degraded && (phase === 'Ready' || (!phase && readiness?.status === 'True'));
  const notReady = !ready && Boolean(degraded || phase || readiness?.status === 'False');
  const status = degraded ? 'Degraded'
    : phase ? formatPhase(phase)
      : readiness?.status === 'True' ? (readiness.type === 'Available' ? 'Available' : 'Ready')
        : readiness?.status === 'False' ? 'Not ready' : 'Unknown';
  return <span className={`status-badge ${ready ? 'ready' : notReady ? 'not-ready' : 'unknown'}`}><span />{status}</span>;
}

function formatPhase(phase?: string): string {
  if (!phase) return '—';
  return phase.replace(/([a-z])([A-Z])/g, '$1 $2');
}

function ConditionList({ conditions }: { conditions?: Condition[] | undefined }) {
  if (!conditions?.length) return <div className="condition-empty">No lifecycle conditions reported.</div>;
  return (
    <div className="condition-list">
      {conditions.map((condition, index) => (
        <div className="condition" key={`${condition.type || 'condition'}-${index}`}>
          <div><strong>{condition.type || 'Condition'}</strong><span>{condition.reason || condition.status || '—'}</span></div>
          {condition.message && <p>{condition.message}</p>}
        </div>
      ))}
    </div>
  );
}

function NodePoolCard({ pool }: { pool: NodePoolResource }) {
  const desired = pool.spec.nodePool?.replicas;
  const current = pool.status?.replicas;
  return (
    <article className="pool-card">
      <div className="pool-card-head"><div><strong>{pool.metadata.name}</strong><span className="muted">Node pool</span></div><StatusBadge conditions={pool.status?.conditions} phase={pool.status?.phase} /></div>
      <div className="pool-metrics">
        <Fact label="Desired replicas" value={displayNumber(desired)} />
        <Fact label="Observed replicas" value={displayNumber(current)} />
        <Fact label="Lifecycle phase" value={formatPhase(pool.status?.phase)} />
      </div>
      {pool.status?.conditions?.length ? <ConditionList conditions={pool.status.conditions} /> : null}
    </article>
  );
}

function displayNumber(value?: number): string {
  return value === undefined ? '—' : new Intl.NumberFormat().format(value);
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div className="fact"><span>{label}</span><strong title={value}>{value}</strong></div>;
}

function Pagination({
  offset, count, hasMore, busy, onChange, compact = false,
}: { offset: number; count: number; hasMore: boolean; busy: boolean; onChange: (offset: number) => void; compact?: boolean }) {
  if (!count && offset === 0) return null;
  return (
    <div className={`pagination ${compact ? 'compact' : ''}`}>
      <span>{offset + 1}–{offset + count}</span>
      <div>
        <button className="button quiet" disabled={busy || offset === 0} onClick={() => onChange(Math.max(0, offset - 50))}>Previous</button>
        <button className="button quiet" disabled={busy || !hasMore} onClick={() => onChange(offset + 50)}>Next</button>
      </div>
    </div>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
