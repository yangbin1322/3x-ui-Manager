import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Input, Modal, Select, Tag } from 'antd';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { Msg } from '@/utils';

interface InstallPanelModalProps {
  open: boolean;
  targets: ManagedServerRecord[];
  fetchVersions: () => Promise<Msg<string[]>>;
  onConfirm: (version: string) => Promise<void> | void;
  onOpenChange: (open: boolean) => void;
}

// Sentinel select values. "latest" maps to an empty version string (the
// installer's default); "custom" reveals a free-text field for an exact tag.
const LATEST = '__latest__';
const CUSTOM = '__custom__';

export default function InstallPanelModal({ open, targets, fetchVersions, onConfirm, onOpenChange }: InstallPanelModalProps) {
  const { t } = useTranslation();
  const [choice, setChoice] = useState<string>(LATEST);
  const [customVersion, setCustomVersion] = useState('');
  const [versions, setVersions] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    setChoice(LATEST);
    setCustomVersion('');
    setSubmitting(false);
    let cancelled = false;
    void (async () => {
      const msg = await fetchVersions();
      if (!cancelled && msg?.success && Array.isArray(msg.obj)) setVersions(msg.obj);
    })();
    return () => { cancelled = true; };
  }, [open, fetchVersions]);

  const options = useMemo(() => [
    { value: LATEST, label: t('pages.servers.versionLatest') },
    ...versions.map((v) => ({ value: v, label: v })),
    { value: CUSTOM, label: t('pages.servers.versionCustom') },
  ], [versions, t]);

  const resolvedVersion = choice === LATEST ? '' : choice === CUSTOM ? customVersion.trim() : choice;
  const canRun = !submitting && (choice !== CUSTOM || customVersion.trim().length > 0);

  async function run() {
    setSubmitting(true);
    try {
      await onConfirm(resolvedVersion);
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

      <Alert
        type="info"
        showIcon
        style={{ marginTop: 12 }}
        message={t('pages.nodes.install.confirmBody')}
      />
    </Modal>
  );
}
