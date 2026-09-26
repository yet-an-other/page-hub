// THROWAWAY. Three inventory layouts on the existing / route, switched with ?variant=A|B|C.
// Question: can the original grouped inventory accommodate observations without crowding out descriptions?
import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { components } from '../../api/generated'
import { sampleInventory } from './fixtures'
import type { InventoryData, Observation, Project, Publication, Scenario } from './fixtures'
import { PrototypeSwitcher } from '../PrototypeSwitcher'
import './prototype.css'

export const variants = [
  { key: 'A', name: 'Inventory', note: 'The original structure. Wide descriptions, compact metadata.' },
  { key: 'B', name: 'Reading list', note: 'Descriptions below titles. No table columns to compete with.' },
  { key: 'C', name: 'Project index', note: 'Choose a Project, then read its Publications at full width.' },
] as const
export type Variant = typeof variants[number]['key']
const stateLabels = { in_sync: 'In sync', drifted: 'Drifted', missing: 'Missing' }

export function Icon({ name, size = 16 }: { name: 'search' | 'chevron' | 'external' | 'refresh' | 'folder' | 'lock' | 'close' | 'info'; size?: number }) {
  const paths = {
    search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></>,
    chevron: <path d="m9 5 7 7-7 7" />,
    external: <><path d="M14 4h6v6m0-6L10 14" /><path d="M10 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5" /></>,
    refresh: <><path d="M20 7v5h-5M4 17v-5h5" /><path d="M6 7a7 7 0 0 1 12-1l2 6M4 12l2 6a7 7 0 0 0 12-1" /></>,
    folder: <path d="M3 7V5h7l2 3h9v12H3V7Z" />,
    lock: <><rect x="5" y="10" width="14" height="11" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2" /></>,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6m0-11v2" /></>,
  }
  return <svg aria-hidden="true" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>
}

function bytes(value: number) {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toLocaleString('en-US', { maximumFractionDigits: 1 })} GiB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MiB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${value} B`
}
function exact(value: number) { return `${value.toLocaleString('en-US')} bytes` }
function pathOf(publication: Publication) { return new URL(publication.canonicalUrl).pathname }
function date(value: string) { return new Date(value).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' }) }
function stamp(value: string) { return `${value.slice(0, 10)} at ${value.slice(11, 16)} UTC` }
function demoHref(publication: Publication, kind: 'preview' | 'public') {
  const url = new URL(location.href)
  url.searchParams.set('demo', kind)
  url.searchParams.set('publication', publication.id)
  return url.href
}

function Status({ publication, onInspect }: { publication: Publication; onInspect: (publication: Publication) => void }) {
  const state = publication.observation?.state
  return <button type="button" className={`ip-status ${state ?? 'unknown'}`} onClick={() => onInspect(publication)} aria-label={`${state ? stateLabels[state] : 'Not checked'}: inspect ${publication.displayName}`} title="View observation details">
    <span className="ip-dot" />{state ? stateLabels[state] : 'Not checked'}<Icon name="info" size={12} />
  </button>
}

function PublicationName({ publication }: { publication: Publication }) {
  return <div className="ip-publication-name">
    <h3>{publication.displayName}</h3>
    <a className="ip-path" href={demoHref(publication, 'preview')} target="_blank" rel="noopener noreferrer" title={`Status-checked preview: ${pathOf(publication)}`}>{pathOf(publication)}</a>
  </div>
}
function PublicLink({ publication }: { publication: Publication }) {
  return <a className="ip-icon-button" href={demoHref(publication, 'public')} target="_blank" rel="noopener noreferrer" aria-label={`Open ${publication.displayName} public page`} title="Open public page"><Icon name="external" /></a>
}
function Description({ publication }: { publication: Publication }) {
  return <p className={`ip-description ${publication.description ? '' : 'ip-empty-description'}`}>{publication.description || 'No private description'}</p>
}

type Group = { project: Project; publications: Publication[] }
type LayoutProps = {
  groups: Group[]
  collapsed: Set<string>
  toggle: (id: string) => void
  searching: boolean
  inspect: (publication: Publication) => void
}
function ProjectHeading({ project, collapsed, toggle, count }: { project: Project; collapsed: boolean; toggle: () => void; count: number }) {
  return <header className="ip-project-header">
    <h2 className="ip-project-heading"><button className="ip-project-toggle" onClick={toggle} aria-expanded={!collapsed} aria-controls={`publications-${project.id}`}>
      <span className={`ip-chevron ${collapsed ? '' : 'expanded'}`}><Icon name="chevron" size={13} /></span>
      <Icon name="folder" size={17} />
      <span className="ip-project-heading-copy"><span className="ip-project-title">{project.displayName}<code>/{project.prefix}/</code></span><span className="ip-project-description">{project.description}</span></span>
    </button></h2>
    <span className="ip-project-count">{count}<span> publication{count === 1 ? '' : 's'}</span></span>
  </header>
}

export function VariantA({ groups, collapsed, toggle, searching, inspect }: LayoutProps) {
  return <div className="ip-table-layout">
    <div className="ip-table-head" aria-hidden="true"><span>Publication</span><span>Private description</span><span>State</span><span>Content changed</span><span>Size</span><span /></div>
    {groups.map(({ project, publications }) => {
      const closed = !searching && collapsed.has(project.id)
      return <section className="ip-project" key={project.id} aria-label={project.displayName}>
        <ProjectHeading project={project} collapsed={closed} toggle={() => toggle(project.id)} count={publications.length} />
        <div id={`publications-${project.id}`} hidden={closed}>
          {!publications.length && <p className="ip-empty">No Publications in this Project yet.</p>}
          {publications.map(publication => <article className="ip-table-row" key={publication.id}>
            <PublicationName publication={publication} />
            <Description publication={publication} />
            <div className="ip-row-state"><Status publication={publication} onInspect={inspect} /></div>
            <time className="ip-row-date" dateTime={publication.contentChangedAt} title={`Content changed ${stamp(publication.contentChangedAt)}`}><span className="ip-mobile-label">Content changed </span>{date(publication.contentChangedAt)}</time>
            <span className="ip-row-size" title={`Accepted size: ${exact(publication.size)}`}>{bytes(publication.size)}</span>
            <PublicLink publication={publication} />
          </article>)}
        </div>
      </section>
    })}
  </div>
}

export function VariantB({ groups, collapsed, toggle, searching, inspect }: LayoutProps) {
  return <div className="ip-reading-layout">
    {groups.map(({ project, publications }) => {
      const closed = !searching && collapsed.has(project.id)
      return <section className="ip-reading-project" key={project.id} aria-label={project.displayName}>
        <ProjectHeading project={project} collapsed={closed} toggle={() => toggle(project.id)} count={publications.length} />
        <div id={`publications-${project.id}`} hidden={closed}>
          {!publications.length && <p className="ip-empty">No Publications in this Project yet.</p>}
          {publications.map(publication => <article className="ip-reading-row" key={publication.id}>
            <div className="ip-reading-copy"><PublicationName publication={publication} /><Description publication={publication} /><div className="ip-reading-meta"><span title={exact(publication.size)}>{bytes(publication.size)}</span><span>Content changed <time dateTime={publication.contentChangedAt} title={stamp(publication.contentChangedAt)}>{date(publication.contentChangedAt)}</time></span></div></div>
            <div className="ip-reading-actions"><Status publication={publication} onInspect={inspect} /><PublicLink publication={publication} /></div>
          </article>)}
        </div>
      </section>
    })}
  </div>
}

export function VariantC({ groups, inspect }: LayoutProps) {
  const [selected, setSelected] = useState('page-hub')
  const group = groups.find(group => group.project.id === selected) ?? groups[0]
  useEffect(() => { console.info('Project index selection', { selectedProject: group?.project.id }) }, [group?.project.id])
  if (!group) return null
  return <div className="ip-index-layout">
    <nav className="ip-project-nav" aria-label="Projects"><p className="ip-small-label">Projects</p>{groups.map(({ project, publications }) => <button key={project.id} onClick={() => setSelected(project.id)} aria-current={project.id === group.project.id ? 'page' : undefined}><Icon name="folder" /><span>{project.displayName}</span><span className="ip-nav-count">{publications.length}</span></button>)}</nav>
    <section className="ip-index-content" aria-label={group.project.displayName}>
      <header className="ip-index-heading"><code>/{group.project.prefix}/</code><h2>{group.project.displayName}</h2><p>{group.project.description}</p><span>{group.publications.length} Publications · {bytes(group.project.publications.reduce((sum, publication) => sum + publication.size, 0))} accepted</span></header>
      {!group.publications.length && <p className="ip-empty">No Publications in this Project yet.</p>}
      {group.publications.map(publication => <article className="ip-index-row" key={publication.id}>
        <div className="ip-index-row-heading"><PublicationName publication={publication} /><PublicLink publication={publication} /></div>
        <Description publication={publication} />
        <div className="ip-index-meta"><Status publication={publication} onInspect={inspect} /><span title={exact(publication.size)}>{bytes(publication.size)}</span><span>Content changed <time dateTime={publication.contentChangedAt} title={stamp(publication.contentChangedAt)}>{date(publication.contentChangedAt)}</time></span></div>
      </article>)}
    </section>
  </div>
}

function Modal({ title, children, close }: { title: string; children: ReactNode; close: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => { ref.current?.showModal() }, [])
  return <dialog className="ip-dialog" ref={ref} onCancel={close} onClick={event => { if (event.target === event.currentTarget) close() }} aria-labelledby="ip-dialog-title">
    <div className="ip-dialog-title"><h2 id="ip-dialog-title">{title}</h2><button className="ip-icon-button" aria-label="Close details" onClick={close}><Icon name="close" /></button></div>{children}
  </dialog>
}
function Fact({ label, children }: { label: string; children: ReactNode }) { return <div><dt>{label}</dt><dd>{children}</dd></div> }

function StorageDetails({ observation, close, version }: { observation: Observation | null; close: () => void; version: string }) {
  return <Modal title="Storage observation" close={close}>
    <p className="ip-dialog-intro">Exact values from the complete bucket listing. Accepted sizes remain catalog values.</p>
    {observation ? <><dl className="ip-facts">
      <Fact label="Bucket usage">{exact(observation.usage.totalBytes)}</Fact><Fact label="Quota">{exact(observation.usage.quotaBytes)}</Fact><Fact label="Accepted Publications">{exact(observation.usage.acceptedBytes)}</Fact><Fact label="Unclaimed storage">{exact(observation.usage.unclaimedBytes)}</Fact><Fact label="Observed">{stamp(observation.observedAt)}</Fact><Fact label="Freshness">{observation.stale ? 'Stale, last successful scan' : 'Current sample observation'}</Fact><Fact label="Storage mutation lock">{observation.mutationLock}</Fact>
    </dl>{observation.mutationLock !== 'none' && <p className="ip-callout">Storage mutations are locked until the findings are classified. Observations do not change accepted content.</p>}</> : <p className="ip-callout">Usage is unavailable until the first successful scan. Accepted Publication sizes are still available.</p>}
    <p className="ip-dialog-foot">Page Hub {version} · Synthetic prototype data</p>
  </Modal>
}
function PublicationDetails({ publication, observation, close }: { publication: Publication; observation: Observation | null; close: () => void }) {
  const state = publication.observation?.state
  return <Modal title={publication.displayName} close={close}>
    <span className={`ip-status ip-static-status ${state ?? 'unknown'}`}><span className="ip-dot" />{state ? stateLabels[state] : 'Not checked'}</span>
    <p className="ip-dialog-intro">{publication.observation?.statusDetail || (state ? 'Stored objects match the accepted manifest.' : 'No successful storage observation yet.')}</p>
    {observation?.stale && <p className="ip-callout">This observation is stale. These are the last successfully checked values.</p>}
    <dl className="ip-facts"><Fact label="Public path"><code>{pathOf(publication)}</code></Fact><Fact label="Entry point"><code>{publication.entryPoint}</code></Fact><Fact label="Routing">{publication.routingMode.replace('_', ' ')}</Fact><Fact label="Accepted size">{exact(publication.size)}</Fact><Fact label="Observed size">{publication.observation?.observedSize == null ? 'Unavailable' : exact(publication.observation.observedSize)}</Fact><Fact label="Content changed">{stamp(publication.contentChangedAt)}</Fact><Fact label="Observed">{observation ? stamp(observation.observedAt) : 'Not yet'}</Fact></dl>
    <p className="ip-small-label">Private description</p><Description publication={publication} />
  </Modal>
}

function SamplePage({ publication, kind }: { publication: Publication; kind: string }) {
  const warning = kind === 'preview' && publication.observation?.state !== 'in_sync'
  return <main className="ip-demo-page"><span className="ip-eyebrow">Prototype link destination</span><h1>{warning ? 'Preview needs attention' : publication.displayName}</h1><p>{warning ? publication.observation?.statusDetail : 'In the real manager, this opens the public Publication in a new tab.'}</p><code>{pathOf(publication)}</code><p>No public content is embedded or fetched by this prototype.</p><button className="ip-button" onClick={() => window.close()}>Close this tab</button></main>
}

function readScenario(): Scenario {
  const value = new URLSearchParams(location.search).get('scenario')
  return ['mixed', 'synced', 'stale', 'failed', 'unobserved'].includes(value ?? '') ? value as Scenario : 'mixed'
}

function readVariant(): Variant {
  const value = new URLSearchParams(location.search).get('variant')?.toUpperCase()
  return variants.some(variant => variant.key === value) ? value as Variant : 'A'
}

export function InventoryPrototype({ inventory, status, onRefresh, pending }: { inventory: InventoryData; status: components['schemas']['ManagerStatus']; onRefresh: () => void; pending: boolean }) {
  const [variant, setVariant] = useState<Variant>(readVariant)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [collapsed, setCollapsed] = useState(new Set<string>())
  const [scenario, setScenario] = useState<Scenario>(readScenario)
  const [storageOpen, setStorageOpen] = useState(false)
  const [selected, setSelected] = useState<Publication | null>(null)
  const [stateOpen, setStateOpen] = useState(false)
  const [refreshed, setRefreshed] = useState(false)
  const data = scenario === 'mixed' ? inventory : sampleInventory(scenario)
  const { observation, projects } = data
  const all = projects.flatMap(project => project.publications)
  const attention = all.filter(publication => publication.observation && publication.observation.state !== 'in_sync').length
  const search = query.trim().toLowerCase()
  const groups = projects.flatMap(project => {
    const projectMatches = [project.displayName, project.prefix, project.description].some(value => value.toLowerCase().includes(search))
    const publications = project.publications.filter(publication => {
      const match = projectMatches || [publication.displayName, publication.path, publication.entryPoint, publication.description].some(value => value.toLowerCase().includes(search))
      return match && (filter === 'all' || (publication.observation && publication.observation.state !== 'in_sync'))
    })
    return publications.length || (projectMatches && filter === 'all' && !project.publications.length) ? [{ project, publications }] : []
  })
  const count = groups.reduce((sum, group) => sum + group.publications.length, 0)
  const toggle = (id: string) => setCollapsed(previous => { const next = new Set(previous); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const changeVariant = (value: Variant) => {
    setVariant(value)
    const url = new URL(location.href)
    url.searchParams.set('variant', value)
    history.replaceState({}, '', url)
  }
  const changeScenario = (value: Scenario) => {
    const url = new URL(location.href)
    url.searchParams.set('scenario', value)
    history.replaceState({}, '', url)
    setScenario(value)
    setFilter('all')
    setRefreshed(false)
  }
  useEffect(() => {
    const back = () => { setVariant(readVariant()); setScenario(readScenario()) }
    window.addEventListener('popstate', back)
    return () => window.removeEventListener('popstate', back)
  }, [])
  useEffect(() => {
    document.title = 'Page Hub · Inventory layout study'
    console.info('Inventory prototype state', { variant, scenario, query, filter, collapsed: [...collapsed], inventory: data })
  }, [variant, scenario, query, filter, collapsed, data])
  const demo = new URLSearchParams(location.search)
  const demoPublication = all.find(publication => publication.id === demo.get('publication'))
  if (demo.get('demo') && demoPublication) return <div className="ip"><SamplePage publication={demoPublication} kind={demo.get('demo')!} /></div>

  const layoutProps = { groups, collapsed, toggle, searching: Boolean(search) || filter !== 'all', inspect: setSelected }
  const layout = variants.find(item => item.key === variant)!
  const checked = observation ? observation.stale ? 'Last checked 2h ago' : refreshed ? 'Checked just now' : 'Checked 2m ago' : 'Not checked yet'

  return <div className={`ip ip-variant-${variant.toLowerCase()}`}>
    <aside className="ip-prototype-note" aria-label="Prototype notice"><span>Layout study <span aria-hidden="true">/</span> synthetic data, no live storage</span><a href="https://share.bdgn.me/page-hub/prototypes/publication-management/?variant=A" target="_blank" rel="noopener noreferrer">First prototype <Icon name="external" size={12} /></a></aside>
    <header className="ip-app-header">
      <a className="ip-brand" href={location.pathname}><span className="ip-brand-mark">P</span><span>Page Hub</span></a>
      <span className="ip-private"><Icon name="lock" size={13} />Private manager</span>
      <div className="ip-header-storage">
        <button className="ip-storage-button" onClick={() => setStorageOpen(true)} aria-label="View exact storage usage and status"><span className={`ip-connectivity ${scenario === 'failed' ? 'ip-connectivity-warning' : ''}`} /><span>{observation ? <><strong>{bytes(observation.usage.totalBytes)}</strong><span className="ip-storage-quota"> / {bytes(observation.usage.quotaBytes)} used</span></> : 'Usage not available'}</span><Icon name="info" size={14} /></button>
        <span className={`ip-last-scan ${observation?.stale ? 'ip-stale' : ''}`}>{pending ? 'Checking storage…' : checked}</span>
        <button className={`ip-icon-button ${pending ? 'ip-refreshing' : ''}`} aria-label="Refresh storage observation" title="Refresh storage observation" onClick={() => { onRefresh(); setRefreshed(true) }} disabled={pending}><Icon name="refresh" /></button>
      </div>
    </header>
    <main className="ip-main">
      <div className="ip-page-heading"><div><p className="ip-eyebrow">Project inventory</p><h1>Your shared pages<span className="ip-heading-count">{all.length}</span></h1><p>Public pages, privately organized.</p></div><span className="ip-description-note"><Icon name="lock" size={13} />Descriptions are private</span></div>
      {scenario === 'failed' ? <div className="ip-alert" role="status"><Icon name="info" /><span>Storage unavailable. Showing the last successful scan.</span><button onClick={() => setStorageOpen(true)}>Details</button></div> : observation?.stale ? <div className="ip-alert" role="status"><Icon name="info" /><span>Observation is stale. Publication states are from the last successful scan.</span><button onClick={() => setStorageOpen(true)}>Details</button></div> : observation?.mutationLock !== 'none' && observation ? <div className="ip-findings"><span className="ip-dot" /><span>{attention} Publications need attention · {bytes(observation.usage.unclaimedBytes)} unclaimed · storage writes locked</span><button onClick={() => setStorageOpen(true)}>View observation <Icon name="chevron" size={12} /></button></div> : null}
      <div className="ip-toolbar">
        <label className="ip-search"><Icon name="search" size={17} /><input type="search" aria-label="Search Projects and Publications" placeholder="Search Projects, paths, descriptions…" value={query} onChange={event => setQuery(event.target.value)} /><kbd>/</kbd></label>
        <div className="ip-filters" aria-label="Publication filters"><button aria-pressed={filter === 'all'} onClick={() => setFilter('all')}>All pages <span>{all.length}</span></button><button aria-pressed={filter === 'attention'} onClick={() => setFilter('attention')}>Needs attention <span>{attention}</span></button></div>
        <span className="ip-results" role="status">{query || filter !== 'all' ? `${count} results` : `${projects.length} Projects`}</span>
      </div>
      {!groups.length ? <div className="ip-no-results"><Icon name="search" size={24} /><h2>No matching Publications</h2><p>Try a name, path, entry point, or private description.</p><button className="ip-button" onClick={() => { setQuery(''); setFilter('all') }}>Clear filters</button></div> : variant === 'A' ? <VariantA {...layoutProps} /> : variant === 'B' ? <VariantB {...layoutProps} /> : <VariantC {...layoutProps} />}
      <footer className="ip-inventory-foot"><span>Sizes show accepted content. Select a state for observation details.</span><span>Page Hub {status.version}</span></footer>
    </main>
    <PrototypeSwitcher current={variant} onChange={changeVariant} openState={() => setStateOpen(true)} />
    {storageOpen && <StorageDetails observation={observation} version={status.version} close={() => setStorageOpen(false)} />}
    {selected && <PublicationDetails publication={selected} observation={observation} close={() => setSelected(null)} />}
    {stateOpen && <Modal title="Prototype controls" close={() => setStateOpen(false)}><p className="ip-dialog-intro"><strong>{variant} · {layout.name}</strong><br />{layout.note}</p><label className="ip-scenario-label">Sample storage state<select value={scenario} onChange={event => changeScenario(event.target.value as Scenario)}><option value="mixed">Drift, missing content, unclaimed storage</option><option value="synced">Everything in sync</option><option value="stale">Stale observation</option><option value="failed">Storage unavailable after a successful scan</option><option value="unobserved">Before the first scan</option></select></label><p className="ip-dialog-foot">All data is synthetic. Refresh and link destinations are simulated. Nothing writes to the catalog or storage.</p><details className="ip-state"><summary>Full current state</summary><pre>{JSON.stringify({ variant, scenario, query, filter, collapsed: [...collapsed], inventory: data }, null, 2)}</pre></details></Modal>}
  </div>
}
