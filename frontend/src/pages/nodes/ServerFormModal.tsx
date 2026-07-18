import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Switch,
  message,
} from 'antd';
import { FormProvider, useForm, useWatch } from 'react-hook-form';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { Msg } from '@/utils';
import { ManagedServerFormSchema, type ManagedServerFormValues } from '@/schemas/managedServer';
import type { SSHTestResult } from '@/generated/types';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import './NodeFormModal.css';

type Mode = 'add' | 'edit';

interface ServerFormModalProps {
  open: boolean;
  mode: Mode;
  server: ManagedServerRecord | null;
  testSSH: (payload: Partial<ManagedServerRecord>, id?: number) => Promise<Msg<SSHTestResult>>;
  save: (payload: Partial<ManagedServerRecord>) => Promise<Msg<unknown>>;
  onOpenChange: (open: boolean) => void;
}

function defaultValues(): ManagedServerFormValues {
  return {
    id: 0,
    name: '',
    remark: '',
    address: '',
    enable: true,
    allowPrivateAddress: false,
    sshPort: 22,
    sshUser: 'root',
    sshAuthType: 'password',
    sshPassword: '',
    sshPrivateKey: '',
    sshKeyPassphrase: '',
    sshHostKeyMode: 'trust',
    sshHostKeySha256: '',
  };
}

export default function ServerFormModal({
  open,
  mode,
  server,
  testSSH,
  save,
  onOpenChange,
}: ServerFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<ManagedServerFormValues>({ defaultValues: defaultValues() });
  const [messageApi, messageContextHolder] = message.useMessage();

  const [submitting, setSubmitting] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<SSHTestResult | null>(null);
  const sshAuthType = useWatch({ control: methods.control, name: 'sshAuthType' }) ?? 'password';

  useEffect(() => {
    if (!open) return;
    const base = defaultValues();
    const next: ManagedServerFormValues = mode === 'edit' && server
      ? {
        ...base,
        ...(server as unknown as Partial<ManagedServerFormValues>),
        id: server.id,
      }
      : base;
    methods.reset(next);
    setTestResult(null);
  }, [open, mode, server, methods]);

  const title = useMemo(
    () => (mode === 'edit' ? t('pages.servers.editServer') : t('pages.servers.addServer')),
    [mode, t],
  );

  function buildPayload(values: ManagedServerFormValues, forTest = false): Partial<ManagedServerRecord> {
    // For a test connection, a trust-mode server must send an empty fingerprint
    // so the host's current key is accepted (and returned) rather than compared
    // against a possibly-stale stored one. For a save, send the form's
    // fingerprint (which a successful test just refreshed) so trust re-anchors
    // to the current key. pin always carries its fingerprint through.
    const fingerprintForSave = forTest && values.sshHostKeyMode === 'trust'
      ? ''
      : values.sshHostKeySha256.trim();
    const payload: Partial<ManagedServerRecord> = {
      id: values.id || 0,
      name: values.name.trim(),
      remark: values.remark?.trim() || '',
      address: values.address.trim(),
      enable: values.enable,
      allowPrivateAddress: values.allowPrivateAddress,
      sshPort: values.sshPort,
      sshUser: values.sshUser.trim(),
      sshAuthType: values.sshAuthType,
      sshHostKeyMode: values.sshHostKeyMode,
      sshHostKeySha256: fingerprintForSave,
    };
    // Credentials are write-only: send them only when the operator entered a
    // value, so an untouched edit keeps the stored secret instead of blanking it.
    const withSecrets = payload as Partial<ManagedServerRecord> & {
      sshPassword?: string; sshPrivateKey?: string; sshKeyPassphrase?: string;
    };
    if (values.sshPassword) withSecrets.sshPassword = values.sshPassword;
    if (values.sshPrivateKey) withSecrets.sshPrivateKey = values.sshPrivateKey;
    if (values.sshKeyPassphrase) withSecrets.sshKeyPassphrase = values.sshKeyPassphrase;
    return withSecrets;
  }

  async function onTestSSH() {
    if (!(await methods.trigger(['address', 'sshUser', 'sshPort']))) return;
    setTesting(true);
    setTestResult(null);
    try {
      const values = methods.getValues();
      const msg = await testSSH(buildPayload(values, true), values.id || undefined);
      if (msg?.success && msg.obj) {
        setTestResult(msg.obj);
        // A successful test is the moment we (re)establish trust: the operator's
        // own credentials authenticated against whatever host key the server now
        // presents. Adopt that fingerprint so saving updates a stale trust-mode
        // anchor (e.g. after the box was reinstalled) — and fills a pin the first
        // time.
        if (msg.obj.success && msg.obj.hostKeySha256 && values.sshHostKeyMode !== 'skip') {
          methods.setValue('sshHostKeySha256', msg.obj.hostKeySha256);
        }
      } else {
        setTestResult({ success: false, panelInstalled: false, message: msg?.msg || t('pages.nodes.connectionFailed') });
      }
    } finally {
      setTesting(false);
    }
  }

  async function onFinish(values: ManagedServerFormValues) {
    const result = ManagedServerFormSchema.safeParse(values);
    if (!result.success) {
      messageApi.error(t(result.error.issues[0]?.message ?? 'pages.nodes.toasts.fillRequired'));
      return;
    }
    setSubmitting(true);
    try {
      // Gate the save on a real SSH handshake. Test with forTest so a
      // trust-mode server accepts the host's current key rather than being
      // blocked by a stale stored fingerprint, then persist whatever key it
      // actually presented so trust re-anchors to the current one.
      const testPayload = buildPayload(result.data, true);
      const test = await testSSH(testPayload, result.data.id || undefined);
      const obj = test?.success ? test.obj : null;
      if (!obj || !obj.success) {
        setTestResult(obj ?? { success: false, panelInstalled: false, message: test?.msg || t('pages.nodes.connectionFailed') });
        return;
      }
      setTestResult(obj);
      const payload = buildPayload(result.data);
      if (obj.hostKeySha256 && result.data.sshHostKeyMode !== 'skip') {
        payload.sshHostKeySha256 = obj.hostKeySha256;
      }
      const msg = await save(payload);
      if (msg?.success) onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  }

  function close() {
    if (!submitting) onOpenChange(false);
  }

  return (
    <>
      {messageContextHolder}
      <Modal
        open={open}
        title={title}
        confirmLoading={submitting}
        okText={t('save')}
        cancelText={t('cancel')}
        mask={{ closable: false }}
        width="640px"
        onOk={methods.handleSubmit(onFinish)}
        onCancel={close}
      >
        <FormProvider {...methods}>
          <Form layout="vertical">
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <FormField
                  label={t('pages.nodes.name')}
                  name="name"
                  rules={{ validate: rhfZodValidate(ManagedServerFormSchema.shape.name) }}
                >
                  <Input />
                </FormField>
              </Col>
              <Col xs={24} md={12}>
                <FormField label={t('pages.nodes.remark')} name="remark">
                  <Input />
                </FormField>
              </Col>
            </Row>

            <Row gutter={16}>
              <Col xs={24} md={12}>
                <FormField
                  label={t('pages.nodes.address')}
                  name="address"
                  rules={{ validate: rhfZodValidate(ManagedServerFormSchema.shape.address) }}
                >
                  <Input placeholder={t('pages.nodes.addressPlaceholder')} />
                </FormField>
              </Col>
              <Col xs={24} md={6}>
                <FormField label={t('pages.nodes.sshPort')} name="sshPort">
                  <InputNumber min={1} max={65535} style={{ width: '100%' }} />
                </FormField>
              </Col>
              <Col xs={24} md={6}>
                <FormField
                  label={t('pages.nodes.sshUser')}
                  name="sshUser"
                  rules={{ validate: rhfZodValidate(ManagedServerFormSchema.shape.sshUser) }}
                >
                  <Input placeholder="root" />
                </FormField>
              </Col>
            </Row>

            <FormField label={t('pages.nodes.sshAuthType')} name="sshAuthType">
              <Select
                options={[
                  { value: 'password', label: t('pages.nodes.sshAuthPassword') },
                  { value: 'key', label: t('pages.nodes.sshAuthKey') },
                ]}
              />
            </FormField>

            {sshAuthType === 'password' ? (
              <FormField
                label={t('pages.nodes.sshPassword')}
                name="sshPassword"
                tooltip={server?.sshPasswordSet ? t('pages.nodes.sshSecretKeepHint') : undefined}
              >
                <Input.Password
                  autoComplete="new-password"
                  placeholder={server?.sshPasswordSet ? t('pages.nodes.sshSecretStored') : ''}
                />
              </FormField>
            ) : (
              <>
                <FormField
                  label={t('pages.nodes.sshPrivateKey')}
                  name="sshPrivateKey"
                  tooltip={server?.sshPrivateKeySet ? t('pages.nodes.sshSecretKeepHint') : undefined}
                >
                  <Input.TextArea
                    rows={4}
                    placeholder={server?.sshPrivateKeySet ? t('pages.nodes.sshSecretStored') : '-----BEGIN OPENSSH PRIVATE KEY-----'}
                  />
                </FormField>
                <FormField label={t('pages.nodes.sshKeyPassphrase')} name="sshKeyPassphrase">
                  <Input.Password autoComplete="new-password" />
                </FormField>
              </>
            )}

            <FormField
              label={t('pages.nodes.sshHostKeyMode')}
              name="sshHostKeyMode"
              tooltip={t('pages.nodes.sshHostKeyModeHint')}
            >
              <Select
                options={[
                  { value: 'trust', label: t('pages.nodes.sshHostKeyTrust') },
                  { value: 'pin', label: t('pages.nodes.sshHostKeyPin') },
                  { value: 'skip', label: t('pages.nodes.sshHostKeySkip') },
                ]}
              />
            </FormField>

            <Row gutter={16}>
              <Col xs={24} md={12}>
                <FormField label={t('pages.nodes.enable')} name="enable" valueProp="checked">
                  <Switch />
                </FormField>
              </Col>
              <Col xs={24} md={12}>
                <FormField
                  label={t('pages.nodes.allowPrivateAddress')}
                  name="allowPrivateAddress"
                  valueProp="checked"
                  tooltip={t('pages.nodes.allowPrivateAddressHint')}
                >
                  <Switch />
                </FormField>
              </Col>
            </Row>

            <div className="test-row">
              <Button type="default" loading={testing} onClick={onTestSSH}>
                {t('pages.nodes.testConnection')}
              </Button>
              {testResult && (
                <div className="test-result">
                  {testResult.success ? (
                    <Alert
                      type="success"
                      showIcon
                      title={t('pages.nodes.sshConnectionOk')}
                      description={[
                        testResult.osName ? `${testResult.osName} ${testResult.osVersion || ''}`.trim() : '',
                        testResult.hostKeySha256 || '',
                      ].filter(Boolean).join(' — ') || undefined}
                    />
                  ) : (
                    <Alert
                      type="error"
                      showIcon
                      title={t('pages.nodes.connectionFailed')}
                      description={testResult.message}
                    />
                  )}
                </div>
              )}
            </div>
          </Form>
        </FormProvider>
      </Modal>
    </>
  );
}
