import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  CheckCircle2,
  CircleAlert,
  CircleSlash,
  GripVertical,
  LockKeyhole,
  Plus,
  Save,
  TestTube2,
  Trash2,
} from "lucide-react";
import {
  adminApi,
  type ExternalService,
  type ExternalServiceInput,
  type ExternalServiceKind,
} from "@/api/admin";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import {
  serviceOptionsByKind,
  servicePresets,
} from "./AdminChannelsPanel.constants";
import { Field, Toggle } from "./AdminModelsPanel.controls";
import { EditorOwnership } from "./editorOwnership";

interface Props {
  services: ExternalService[];
  setServices: Dispatch<SetStateAction<ExternalService[]>>;
  setError: (error: string) => void;
  onDirtyChange?: (dirty: boolean) => void;
}

type ChainKind = Exclude<ExternalServiceKind, "ocr">;
type ChainStatus = "ready" | "disabled" | "incomplete";

function interactionLocked(
  open: boolean,
  saving: boolean,
  testing: boolean,
  reorderingKind: ChainKind | null,
  deleteTarget: ExternalService | null,
  deletingKey: string,
) {
  return open || saving || testing || reorderingKind !== null || deleteTarget !== null || deletingKey !== "";
}

function emptyServiceDraft(
  key: string,
  sortOrder: number,
): ExternalServiceInput {
  const preset = servicePresets[key];
  return {
    key,
    display_name: preset?.label || key,
    kind: preset?.kind || "search",
    base_url: preset?.baseURL || "",
    api_key: "",
    enabled: true,
    sort_order: sortOrder,
    max_concurrency: preset?.maxConcurrency || 0,
  };
}

function serviceDraftFrom(item: ExternalService): ExternalServiceInput {
  const preset = servicePresets[item.key];
  return {
    key: item.key,
    display_name: preset?.label || item.display_name,
    kind: item.kind,
    base_url: item.base_url || preset?.baseURL || "",
    api_key: "",
    enabled: item.enabled,
    sort_order: item.sort_order,
    max_concurrency: item.max_concurrency || preset?.maxConcurrency || 0,
  };
}

export function AdminExternalServiceChain({
  services,
  setServices,
  setError,
  onDirtyChange,
}: Props) {
  const [draft, setDraft] = useState<ExternalServiceInput>(() =>
    emptyServiceDraft("tavily_search", 10),
  );
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testMessage, setTestMessage] = useState("");
  const [reorderingKind, setReorderingKind] = useState<ChainKind | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ExternalService | null>(
    null,
  );
  const [deletingKey, setDeletingKey] = useState("");
  const [editorOwner] = useState(() => new EditorOwnership());
  const [deleteOwner] = useState(() => new EditorOwnership());
  const mountedRef = useRef(true);
  const panelDirty = editorOwner.isDirty();

  useEffect(() => onDirtyChange?.(panelDirty), [onDirtyChange, panelDirty]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: 140, tolerance: 5 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const grouped = useMemo(
    () => ({
      search: orderedServices(services, "search"),
      crawler: orderedServices(services, "crawler"),
      ocr: orderedServices(services, "ocr"),
    }),
    [services],
  );

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      editorOwner.invalidate();
      deleteOwner.invalidate();
    };
  }, [deleteOwner, editorOwner]);

  function changeDraft(update: SetStateAction<ExternalServiceInput>) {
    editorOwner.change();
    setTestMessage("");
    setDraft(update);
  }

  function closeEditor() {
    if (editorOwner.isDirty() && !window.confirm("放弃当前外部服务的未保存修改？")) return;
    editorOwner.invalidate();
    setSaving(false);
    setTesting(false);
    setTestMessage("");
    setOpen(false);
  }

  function openEditor(
    service?: ExternalService,
    key?: string,
    kind?: ExternalServiceKind,
  ) {
    if (interactionLocked(open, saving, testing, reorderingKind, deleteTarget, deletingKey)) return;
    const nextKey = key || service?.key || "tavily_search";
    const chain = kind || service?.kind || "search";
    const nextOrder =
      service?.sort_order ||
      Math.max(0, ...grouped[chain].map((item) => item.sort_order)) + 10;
    setDraft(
      service
        ? serviceDraftFrom(service)
        : emptyServiceDraft(nextKey, nextOrder),
    );
    editorOwner.activate(nextKey);
    setSaving(false);
    setTesting(false);
    setTestMessage("");
    setOpen(true);
  }

  async function saveService() {
    const currentDraft = draft;
    const operation = editorOwner.beginOperation();
    setSaving(true);
    setError("");
    try {
      const saved = await adminApi.saveExternalService({
        ...currentDraft,
        api_key: currentDraft.api_key?.trim() || undefined,
      });
      // Persisted mutations always converge the shared chain; only dialog-local
      // state is fenced when the user edits further or opens another service.
      setServices((current) =>
        sortServices(
          current.some((item) => item.key === saved.key)
            ? current.map((item) => (item.key === saved.key ? saved : item))
            : [...current, saved],
        ),
      );
      if (editorOwner.owns(operation, false)) {
        editorOwner.acknowledge(operation.revision);
        if (editorOwner.owns(operation)) {
          setSaving(false);
          editorOwner.invalidate();
          setOpen(false);
        } else {
          setError("已保存较早版本，当前修改仍未保存");
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "外部服务保存失败");
      }
    } finally {
      if (mountedRef.current && editorOwner.owns(operation, false)) setSaving(false);
    }
  }

  async function testDraft() {
    const currentDraft = draft;
    const operation = editorOwner.beginOperation();
    setTesting(true);
    setTestMessage("");
    try {
      const result = await adminApi.testExternalService({
        ...currentDraft,
        api_key: currentDraft.api_key?.trim() || undefined,
      });
      if (editorOwner.owns(operation)) {
        setTestMessage(
          result.ok
            ? `连接成功${result.duration_ms ? ` · ${result.duration_ms}ms` : ""}`
            : result.error || "连接失败",
        );
      }
    } catch (err) {
      if (editorOwner.owns(operation)) {
        setTestMessage(err instanceof Error ? err.message : "连接失败");
      }
    } finally {
      if (mountedRef.current && editorOwner.owns(operation, false)) setTesting(false);
    }
  }

  async function handleDragEnd(kind: ChainKind, event: DragEndEvent) {
    if (reorderingKind || open || saving || testing || deleteTarget || deletingKey) return;
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const previous = grouped[kind];
    const oldIndex = previous.findIndex((item) => item.key === active.id);
    const newIndex = previous.findIndex((item) => item.key === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove(previous, oldIndex, newIndex);
    setServices((current) =>
      sortServices(applyChainOrder(current, kind, reordered)),
    );
    setReorderingKind(kind);
    try {
      const response = await adminApi.reorderExternalServices(
        kind,
        reordered.map((item) => item.key),
      );
      setServices((current) =>
        sortServices(applyChainOrder(current, kind, response.services)),
      );
    } catch (err) {
      setServices((current) =>
        sortServices(applyChainOrder(current, kind, previous)),
      );
      setError(err instanceof Error ? err.message : "服务排序保存失败");
    } finally {
      setReorderingKind(null);
    }
  }

  async function deleteService() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    const operation = deleteOwner.beginOperation();
    setDeletingKey(target.key);
    setError("");
    try {
      await adminApi.deleteExternalService(target.key);
      setServices((current) =>
        current.filter((service) => service.key !== target.key),
      );
      if (deleteOwner.owns(operation, false)) {
        setDeletingKey("");
        setDeleteTarget(null);
        deleteOwner.invalidate();
      }
    } catch (err) {
      if (deleteOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "服务删除失败");
      }
    } finally {
      if (mountedRef.current && deleteOwner.owns(operation, false)) setDeletingKey("");
    }
  }

  function requestDelete(service: ExternalService) {
    deleteOwner.activate(service.key);
    setDeleteTarget(service);
  }

  return (
    <section className="min-h-0 overflow-y-auto">
      <div className="border-b border-border/70 px-3 py-3 text-sm font-medium">
        搜索与网页提取
      </div>
      <div className="divide-y divide-border/70 px-3">
        <ServiceChain
          title="搜索"
          kind="search"
          services={grouped.search}
          configured={services}
          sensors={sensors}
          onAdd={(key) => openEditor(undefined, key, "search")}
          onEdit={openEditor}
          onDelete={requestDelete}
          onDragEnd={handleDragEnd}
          busy={interactionLocked(open, saving, testing, reorderingKind, deleteTarget, deletingKey)}
        />
        <ServiceChain
          title="网页提取"
          kind="crawler"
          services={grouped.crawler}
          configured={services}
          sensors={sensors}
          onAdd={(key) => openEditor(undefined, key, "crawler")}
          onEdit={openEditor}
          onDelete={requestDelete}
          onDragEnd={handleDragEnd}
          busy={interactionLocked(open, saving, testing, reorderingKind, deleteTarget, deletingKey)}
          showBasic
        />
        <OCRSection
          services={grouped.ocr}
          onEdit={openEditor}
          onDelete={requestDelete}
          busy={interactionLocked(open, saving, testing, reorderingKind, deleteTarget, deletingKey)}
        />
      </div>
      <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) closeEditor(); }}>
        <DialogContent className="max-w-xl rounded-md">
          <DialogHeader>
            <DialogTitle>
              {servicePresets[draft.key]?.label ||
                draft.display_name ||
                "服务配置"}
            </DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <Field label="Base URL">
              <Input
                value={draft.base_url}
                placeholder={servicePresets[draft.key]?.baseURL || "https://"}
                onChange={(event) =>
                  changeDraft((current) => ({
                    ...current,
                    base_url: event.target.value,
                  }))
                }
              />
            </Field>
            <Field label={draft.key === "mineru" ? "Token" : "API key"}>
              <Input
                type="password"
                value={draft.api_key || ""}
                placeholder={
                  servicePresets[draft.key]?.keyOptional
                    ? "可留空；保存时留空保留现有 Key"
                    : "保存时留空保留现有 Key"
                }
                onChange={(event) =>
                  changeDraft((current) => ({
                    ...current,
                    api_key: event.target.value,
                  }))
                }
              />
            </Field>
            {draft.key === "mineru" ? (
              <Field label="最大并发">
                <Input
                  type="number"
                  min={1}
                  max={20}
                  value={draft.max_concurrency || 2}
                  onChange={(event) =>
                    changeDraft((current) => ({
                      ...current,
                      max_concurrency: clampConcurrency(event.target.value),
                    }))
                  }
                />
              </Field>
            ) : null}
            {testMessage ? (
              <div
                className={cn(
                  "text-xs",
                  testMessage.startsWith("连接成功")
                    ? "text-emerald-600"
                    : "text-destructive",
                )}
              >
                {testMessage}
              </div>
            ) : null}
          </div>
          <DialogFooter className="items-center justify-between sm:justify-between">
            <Toggle
              label="状态"
              checked={draft.enabled}
              onChange={(enabled) =>
                changeDraft((current) => ({ ...current, enabled }))
              }
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => void testDraft()}
                disabled={saving || testing}
              >
                <TestTube2 className="h-3.5 w-3.5" />
                {testing ? "测试中" : "测试"}
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={() => void saveService()}
                disabled={saving || testing}
              >
                <Save className="h-3.5 w-3.5" />
                保存
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !deletingKey) {
            deleteOwner.invalidate();
            setDeleteTarget(null);
          }
        }}
      >
        <DialogContent className="max-w-sm rounded-md">
          <DialogHeader>
            <DialogTitle>删除服务</DialogTitle>
            <DialogDescription>
              删除{" "}
              {servicePresets[deleteTarget?.key || ""]?.label ||
                deleteTarget?.display_name ||
                "此服务"}{" "}
              后，将不再参与后续调用。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={Boolean(deletingKey)}
              onClick={() => setDeleteTarget(null)}
            >
              取消
            </Button>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              disabled={Boolean(deletingKey)}
              onClick={() => void deleteService()}
            >
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

function ServiceChain({
  title,
  kind,
  services,
  configured,
  sensors,
  onAdd,
  onEdit,
  onDelete,
  onDragEnd,
  busy,
  showBasic,
}: {
  title: string;
  kind: ChainKind;
  services: ExternalService[];
  configured: ExternalService[];
  sensors: ReturnType<typeof useSensors>;
  onAdd: (key: string) => void;
  onEdit: (
    service?: ExternalService,
    key?: string,
    kind?: ExternalServiceKind,
  ) => void;
  onDelete: (service: ExternalService) => void;
  onDragEnd: (kind: ChainKind, event: DragEndEvent) => void;
  busy: boolean;
  showBasic?: boolean;
}) {
  const options = serviceOptionsByKind[kind].filter(
    (key) => !configured.some((service) => service.key === key),
  );
  return (
    <div className="py-4">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-sm font-medium">{title}</div>
        <Popover>
          <PopoverTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-xs"
              disabled={busy}
            >
              <Plus className="h-3.5 w-3.5" />
              添加服务
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-52 p-1">
            {options.length ? (
              options.map((key) => (
                <button
                  key={key}
                  type="button"
                  className="flex w-full items-center px-2 py-2 text-left text-sm hover:bg-muted"
                  onClick={() => onAdd(key)}
                  disabled={busy}
                >
                  {servicePresets[key].label}
                </button>
              ))
            ) : (
              <div className="px-2 py-2 text-xs text-muted-foreground">
                所有服务已添加
              </div>
            )}
          </PopoverContent>
        </Popover>
      </div>
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={(event) => void onDragEnd(kind, event)}
      >
        <SortableContext
          items={services.map((service) => service.key)}
          strategy={verticalListSortingStrategy}
        >
          <div className="divide-y divide-border/70 border-y border-border/70">
            {services.length ? (
              services.map((service) => (
                <SortableServiceRow
                  key={service.key}
                  service={service}
                  onEdit={() => onEdit(service)}
                  onDelete={() => onDelete(service)}
                  disabled={busy}
                />
              ))
            ) : (
              <div className="px-2 py-3 text-xs text-muted-foreground">
                暂无已配置服务
              </div>
            )}
            {showBasic ? <BuiltinRow /> : null}
          </div>
        </SortableContext>
      </DndContext>
    </div>
  );
}

function SortableServiceRow({
  service,
  onEdit,
  onDelete,
  disabled,
}: {
  service: ExternalService;
  onEdit: () => void;
  onDelete: () => void;
  disabled: boolean;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: service.key, disabled });
  const preset = servicePresets[service.key];
  const status = serviceStatus(service);
  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cn(
        "flex min-w-0 items-center gap-2 px-2 py-2",
        isDragging && "relative z-10 bg-background shadow-md",
      )}
    >
      <button
        type="button"
        className="flex h-8 w-7 shrink-0 cursor-grab items-center justify-center touch-none text-muted-foreground hover:text-foreground active:cursor-grabbing disabled:cursor-not-allowed disabled:opacity-50"
        aria-label={`调整 ${preset?.label || service.display_name} 顺序`}
        disabled={disabled}
        {...attributes}
        {...listeners}
      >
        <GripVertical className="h-4 w-4" />
      </button>
      <StatusIcon status={status} />
      <button
        type="button"
        className="min-w-0 flex-1 text-left disabled:cursor-not-allowed disabled:opacity-50"
        onClick={onEdit}
        disabled={disabled}
      >
        <span className="block truncate text-sm font-medium">
          {preset?.label || service.display_name}
        </span>
        <span className="block truncate text-xs text-muted-foreground">
          {service.base_url || "默认地址"}
        </span>
      </button>
      <span className={cn("shrink-0 text-xs", statusTextClass(status))}>
        {statusLabel(status)}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="h-8 w-8 shrink-0 text-muted-foreground hover:text-destructive"
        aria-label={`删除 ${preset?.label || service.display_name}`}
        title={`删除 ${preset?.label || service.display_name}`}
        onClick={onDelete}
        disabled={disabled}
      >
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}

function OCRSection({
  services,
  onEdit,
  onDelete,
  busy,
}: {
  services: ExternalService[];
  onEdit: (
    service?: ExternalService,
    key?: string,
    kind?: ExternalServiceKind,
  ) => void;
  onDelete: (service: ExternalService) => void;
  busy: boolean;
}) {
  const service = services.find((item) => item.key === "mineru");
  return (
    <div className="py-4">
      <div className="mb-2 text-sm font-medium">文档 OCR</div>
      <div className="divide-y divide-border/70 border-y border-border/70">
        {service ? (
          <div className="flex items-center gap-2 px-2 py-2">
            <StatusIcon status={serviceStatus(service)} />
            <button
              type="button"
              className="min-w-0 flex-1 text-left disabled:cursor-not-allowed disabled:opacity-50"
              onClick={() => onEdit(service)}
              disabled={busy}
            >
              <span className="block text-sm font-medium">MinerU 精准解析</span>
              <span className="block truncate text-xs text-muted-foreground">
                {service.base_url}
              </span>
            </button>
            <span
              className={cn("text-xs", statusTextClass(serviceStatus(service)))}
            >
              {statusLabel(serviceStatus(service))}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-8 w-8 shrink-0 text-muted-foreground hover:text-destructive"
              aria-label="删除 MinerU 精准解析"
              title="删除 MinerU 精准解析"
              onClick={() => onDelete(service)}
              disabled={busy}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ) : (
          <button
            type="button"
            className="flex w-full items-center gap-2 px-2 py-3 text-left text-xs text-muted-foreground hover:bg-muted/40 disabled:cursor-not-allowed disabled:opacity-50"
            onClick={() => onEdit(undefined, "mineru", "ocr")}
            disabled={busy}
          >
            <Plus className="h-3.5 w-3.5" />
            配置 MinerU
          </button>
        )}
      </div>
    </div>
  );
}

function BuiltinRow() {
  return (
    <div className="flex items-center gap-2 px-2 py-2 text-muted-foreground">
      <span className="flex h-8 w-7 items-center justify-center">
        <LockKeyhole className="h-3.5 w-3.5" />
      </span>
      <span className="min-w-0 flex-1 text-sm">Basic 提取</span>
      <span className="text-xs">固定兜底</span>
    </div>
  );
}

function StatusIcon({ status }: { status: ChainStatus }) {
  if (status === "ready")
    return <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" />;
  if (status === "disabled")
    return <CircleSlash className="h-4 w-4 shrink-0 text-muted-foreground" />;
  return <CircleAlert className="h-4 w-4 shrink-0 text-amber-500" />;
}
function serviceStatus(service: ExternalService): ChainStatus {
  if (!service.enabled) return "disabled";
  const preset = servicePresets[service.key];
  if (
    (service.key === "searxng" && !service.base_url.trim()) ||
    (!preset?.keyOptional &&
      service.key !== "mineru" &&
      !service.api_key_set) ||
    (service.key === "mineru" &&
      (!service.base_url.trim() || !service.api_key_set))
  )
    return "incomplete";
  return "ready";
}
function statusLabel(status: ChainStatus) {
  if (status === "ready") return "可用";
  if (status === "disabled") return "停用";
  return "缺配置";
}
function statusTextClass(status: ChainStatus) {
  return status === "ready"
    ? "text-emerald-600"
    : status === "incomplete"
      ? "text-amber-600"
      : "text-muted-foreground";
}
function orderedServices(
  services: ExternalService[],
  kind: ExternalServiceKind,
) {
  return services
    .filter(
      (service) =>
        service.kind === kind &&
        !(kind === "crawler" && service.key === "basic"),
    )
    .sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key));
}
function applyChainOrder(
  services: ExternalService[],
  kind: ChainKind,
  ordered: ExternalService[],
) {
  const orderByKey = new Map(
    ordered.map((service, index) => [service.key, (index + 1) * 10]),
  );
  return services.map((service) => {
    const sortOrder =
      kind === service.kind ? orderByKey.get(service.key) : undefined;
    return sortOrder === undefined
      ? service
      : { ...service, sort_order: sortOrder };
  });
}
function sortServices(services: ExternalService[]) {
  return [...services].sort(
    (a, b) =>
      a.kind.localeCompare(b.kind) ||
      a.sort_order - b.sort_order ||
      a.key.localeCompare(b.key),
  );
}
function clampConcurrency(value: string) {
  const parsed = Number(value);
  return Number.isFinite(parsed)
    ? Math.min(20, Math.max(1, Math.floor(parsed)))
    : 1;
}
