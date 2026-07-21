import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Card, Divider, Input, InputNumber, Modal, Segmented, Select, Tag } from 'antd';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { InstallConfig } from '@/generated/types';
import type { Msg } from '@/utils';

interface InstallPanelModalProps {
  open: boolean;
  targets: ManagedServerRecord[];
  fetchVersions: () => Promise<Msg<string[]>>;
  onConfirm: (version: string, config: InstallConfig) => Promise<void> | void;
  onOpenChange: (open: boolean) => void;
}

// Sentinel select values. "latest" maps to an empty version string (the
// installer's default); "custom" reveals a free-text field for an exact tag.
const LATEST = '__latest__';
const CUSTOM = '__custom__';

type DbType = 'sqlite' | 'postgres';
type SslMode = 'none' | 'ip' | 'domain';

export default function InstallPanelModal({ open, targets, fetchVersions, onConfirm, onOpenChange }: InstallPanelModalProps) {
  const { t } = useTranslation();
  const [choice, setChoice] = useState<string>(LATEST);
  const [customVersion, setCustomVersion] = useState('');
  const [versions, setVersions] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);

  // Install config. Blank text fields mean "let install.sh pick" (random
  // credentials/port/path). dbType/sslMode always have an explicit default.
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [panelPort, setPanelPort] = useState<number | null>(null);
  const [webBasePath, setWebBasePath] = useState('');
  const [dbType, setDbType] = useState<DbType>('sqlite');
  const [sslMode, setSslMode] = useState<SslMode>('none');
  const [domain, setDomain] = useState('');

  // fetchVersions changes identity on every parent re-render (heartbeat refresh);
  // hold it in a ref so the open-effect can call it without depending on it.
  const fetchVersionsRef = useRef(fetchVersions);
  fetchVersionsRef.current = fetchVersions;

  useEffect(() => {
    if (!open) return;
    setChoice(LATEST);
    setCustomVersion('');
    setSubmitting(false);
    setUsername('');
    setPassword('');
    setPanelPort(null);
    setWebBasePath('');
    setDbType('sqlite');
    setSslMode('none');
    setDomain('');
    let cancelled = false;
    void (async () => {
      const msg = await fetchVersionsRef.current();
      if (!cancelled && msg?.success && Array.isArray(msg.obj)) setVersions(msg.obj);
    })();
    return () => { cancelled = true; };
    // Reset only when the modal opens. fetchVersions is intentionally excluded:
    // it changes identity on every parent re-render (e.g. the heartbeat refresh),
    // and depending on it would re-run this effect and wipe the form the operator
    // is filling in. It is read through a ref instead.
  }, [open]);

  const options = useMemo(() => [
    { value: LATEST, label: t('pages.servers.versionLatest') },
    ...versions.map((v) => ({ value: v, label: v })),
    { value: CUSTOM, label: t('pages.servers.versionCustom') },
  ], [versions, t]);

  const resolvedVersion = choice === LATEST ? '' : choice === CUSTOM ? customVersion.trim() : choice;
  const config: InstallConfig = {
    username: username.trim(),
    password: password.trim(),
    panelPort: panelPort ? String(panelPort) : '',
    webBasePath: webBasePath.trim(),
    dbType,
    sslMode,
    domain: domain.trim(),
  };
  const domainRequiredMissing = sslMode === 'domain' && domain.trim().length === 0;
  const canRun = !submitting && (choice !== CUSTOM || customVersion.trim().length > 0) && !domainRequiredMissing;

  async function run() {
    setSubmitting(true);
    try {
      await onConfirm(resolvedVersion, config);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  }

  const title = targets.length === 1
    ? t('pages.nodes.install.confirmTitle', { name: targets[0]?.name || `#${targets[0]?.id}` })
    : t('pages.servers.batchInstallConfirmTitle', { count: targets.length });

  return (
    <Modal
      open={open}
      title={title}
      width="640px"
      okText={t('pages.nodes.install.action')}
      cancelText={t('cancel')}
      okButtonProps={{ disabled: !canRun, loading: submitting }}
      onOk={run}
      onCancel={() => { if (!submitting) onOpenChange(false); }}
      maskClosable={false}
    >
      {targets.length > 1 && (
        <div style={{ marginBottom: 12 }}>
          {targets.map((s) => (
            <Tag key={s.id} color="processing" style={{ marginBottom: 4 }}>{s.name || `#${s.id}`}</Tag>
          ))}
        </div>
      )}

      <label style={{ display: 'block', marginBottom: 6 }}>{t('pages.servers.installVersion')}</label>
      <Select
        value={choice}
        onChange={setChoice}
        options={options}
        style={{ width: '100%' }}
        disabled={submitting}
      />
      {choice === CUSTOM && (
        <Input
          style={{ marginTop: 8 }}
          value={customVersion}
          onChange={(e) => setCustomVersion(e.target.value)}
          placeholder={t('pages.servers.versionPlaceholder')}
          disabled={submitting}
        />
      )}

      <Card size="small" style={{ marginTop: 12 }} title={t('pages.nodes.install.config.title')}>
        <div style={{ opacity: 0.65, marginBottom: 10, fontSize: 12 }}>{t('pages.nodes.install.config.hint')}</div>

        <Divider style={{ margin: '4px 0 10px' }}>{t('pages.nodes.install.config.panelSection')}</Divider>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('pages.nodes.install.config.username')} disabled={submitting} />
          <Input value={password} onChange={(e) => setPassword(e.target.value)} placeholder={t('pages.nodes.install.config.password')} disabled={submitting} />
          <InputNumber value={panelPort} onChange={(v) => setPanelPort(typeof v === 'number' ? v : null)} min={1} max={65535} placeholder={t('pages.nodes.install.config.panelPort')} style={{ width: '100%' }} disabled={submitting} />
          <Input value={webBasePath} onChange={(e) => setWebBasePath(e.target.value)} placeholder={t('pages.nodes.install.config.webBasePath')} disabled={submitting} />
        </div>

        <Divider style={{ margin: '14px 0 10px' }}>{t('pages.nodes.install.config.dbSection')}</Divider>
        <Segmented<DbType>
          value={dbType}
          onChange={setDbType}
          options={[
            { value: 'sqlite', label: t('pages.nodes.install.config.dbSqlite') },
            { value: 'postgres', label: t('pages.nodes.install.config.dbPostgres') },
          ]}
          disabled={submitting}
        />

        <Divider style={{ margin: '14px 0 10px' }}>{t('pages.nodes.install.config.sslSection')}</Divider>
        <Segmented<SslMode>
          value={sslMode}
          onChange={setSslMode}
          options={[
            { value: 'none', label: t('pages.nodes.install.config.sslNone') },
            { value: 'ip', label: t('pages.nodes.install.config.sslIp') },
            { value: 'domain', label: t('pages.nodes.install.config.sslDomain') },
          ]}
          disabled={submitting}
        />
        {sslMode === 'domain' && (
          <Input
            style={{ marginTop: 8 }}
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            placeholder={t('pages.nodes.install.config.domainPlaceholder')}
            status={domainRequiredMissing ? 'error' : undefined}
            disabled={submitting}
          />
        )}
        {sslMode !== 'none' && (
          <div style={{ opacity: 0.65, marginTop: 6, fontSize: 12 }}>{t('pages.nodes.install.config.sslHint')}</div>
        )}
      </Card>

      <Alert
        type="info"
        showIcon
        style={{ marginTop: 12 }}
        message={t('pages.nodes.install.confirmBody')}
      />
    </Modal>
  );
}
