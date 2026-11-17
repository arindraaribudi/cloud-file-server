import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Definition,
  Step,
  StepsConfiguration,
  ToolboxConfiguration,
  ValidatorConfiguration,
} from "sequential-workflow-designer";
import {
  SequentialWorkflowDesigner,
  useStepEditor,
  wrapDefinition,
  type WrappedDefinition,
} from "sequential-workflow-designer-react";
import "sequential-workflow-designer/css/designer.css";
import "sequential-workflow-designer/css/designer-light.css";
import type { ComponentType, WorkflowComponentInput, WorkflowStage } from "../../lib/workflows";
import {
  CONDITION_KEY,
  DEFAULT_CONDITION,
  GROUPS,
  boxName,
  conditionTitle,
  groupTitle,
  isTrivialCondition,
  uiName,
} from "../../lib/workflowUi";
import { STEP_ICONS } from "../../lib/workflowIcons";
import { EditorHost } from "./EditorHost";
import { SchemaForm } from "./SchemaForm";
import { routeToggleExtension, toggleRouteHandler } from "./routeToggle";

/*
 * Canvas model
 *
 *   Trigger  ->  Condition box  ->  [ process steps ]
 *                                   [ Copy to folders   : Folder A, Folder B, ... ]
 *                                   [ Webhook / API calls: Call A, Call B, ...   ]
 *                                   [ Email              : ...                   ]
 *
 * The backend stores a flat, ordered list of components. The tree is derived
 * from that list (and flattened back on every change), so grouping never needs
 * to be stored and a step dropped in the wrong place snaps into the right box.
 */

type PropertyValue =
  | string
  | string[]
  | number
  | number[]
  | boolean
  | boolean[]
  | null
  | object;

interface WorkflowDefinition extends Definition {
  properties: Record<string, PropertyValue>;
}

interface WorkflowStep extends Step {
  properties: {
    stage: WorkflowStage;
    typeKey: string;
    typeVersion: number;
    config: Record<string, unknown>;
    enabled: boolean;
    [key: string]: PropertyValue;
  };
}

interface BoxStep extends Step {
  componentType: "container";
  sequence: Step[];
}

export type EditableComponent = WorkflowComponentInput & { clientId: string };

// Module-level so the designer is not rebuilt because of a new array identity.
const ROUTE_TOGGLE_EXTENSIONS = [routeToggleExtension];

const CONDITION_BOX_ID = "condition-box";
const CONDITION_COMPONENT_ID = "condition-component";

export function WorkflowDesigner({
  components,
  catalog,
  onChange,
}: {
  components: EditableComponent[];
  catalog: ComponentType[];
  onChange: (components: EditableComponent[]) => void;
}) {
  const [definition, setDefinition] = useState<WrappedDefinition<WorkflowDefinition>>(() =>
    wrapDefinition(toDefinition(components)),
  );
  const [selectedStepId, setSelectedStepId] = useState<string | null>(null);
  // Handed to the editors, which live in a React root the designer creates.
  const queryClient = useQueryClient();
  const knownIds = useRef(new Set(components.map((component) => component.clientId)));
  const hadTrigger = useRef(components.some((component) => component.stage === "trigger"));

  const catalogByKey = useMemo(() => new Map(catalog.map((t) => [t.key, t])), [catalog]);
  const safeToKey = useMemo(
    () => new Map(catalog.map((t) => [safeType(t.key), t.key])),
    [catalog],
  );
  const hasTrigger = components.some((component) => component.stage === "trigger");

  const toolboxConfiguration = useMemo<ToolboxConfiguration>(
    () => buildToolbox(catalog, catalogByKey, safeToKey, hasTrigger),
    [catalog, catalogByKey, safeToKey, hasTrigger],
  );
  const stepsConfiguration = useMemo<StepsConfiguration>(
    () => ({
      iconUrlProvider: (_componentType, type) => STEP_ICONS[type] ?? null,
      // Action boxes are derived from their steps; Condition boxes (routes) can
      // be moved, duplicated and deleted like any other step.
      isDeletable: (step) => step.properties.role !== "group",
      isDraggable: (step) => step.properties.role !== "group",
      isDuplicable: (step) => step.properties.role !== "group",
    }),
    [],
  );
  // A red marker appears on steps that still miss a required setting.
  const validatorConfiguration = useMemo<ValidatorConfiguration>(
    () => ({
      step: (step) => {
        if (!step.name?.trim()) return false;
        if (step.componentType === "container") return true;
        const typeKey =
          typeof step.properties.typeKey === "string"
            ? step.properties.typeKey
            : safeToKey.get(step.type);
        const schema = typeKey ? catalogByKey.get(typeKey)?.config_schema : undefined;
        if (!schema) return true;
        const config = isRecord(step.properties.config) ? step.properties.config : {};
        return (schema.required ?? []).every((key) =>
          hasValue(config[key] !== undefined ? config[key] : schema.properties?.[key]?.default),
        );
      },
    }),
    [catalogByKey, safeToKey],
  );

  function handleDefinitionChange(next: WrappedDefinition<WorkflowDefinition>) {
    const extracted = fromDefinition(next.value, catalogByKey, safeToKey);
    const raw = normalizeParallel(extracted.components);
    // Only number repeated default names when a step has just been added, so
    // renaming a step never changes its label underneath the user.
    const added = raw.some((component) => !knownIds.current.has(component.clientId));
    const flat = added ? numberDuplicateNames(raw, catalogByKey) : raw;
    knownIds.current = new Set(flat.map((component) => component.clientId));
    const relabelled = flat.some((component, index) => component.label !== raw[index]?.label);
    const normalized = toDefinition(flat, extracted.boxes);
    onChange(flat);
    // The toolbox hides the Trigger group while one exists. The designer only
    // picks up a new toolbox when it is rebuilt, so rebuild when that changes.
    const hasTriggerNow = flat.some((component) => component.stage === "trigger");
    const triggerChanged = hasTriggerNow !== hadTrigger.current;
    hadTrigger.current = hasTriggerNow;
    if (triggerChanged || relabelled || !sameStructure(next.value, normalized)) {
      if (selectedStepId && !hasStep(normalized.sequence, selectedStepId)) {
        setSelectedStepId(null);
      }
      setDefinition(wrapDefinition(normalized));
    } else {
      setDefinition(next);
    }
  }

  // Collapse / expand every route at once (only useful with more than one).
  const routeSteps = definition.value.sequence.filter((step) => step.properties.role === "condition");
  const allCollapsed = routeSteps.length > 0 && routeSteps.every((step) => step.componentType !== "container");

  function setAllCollapsed(collapsed: boolean) {
    const extracted = fromDefinition(definition.value, catalogByKey, safeToKey);
    const next = toDefinition(
      normalizeParallel(extracted.components),
      extracted.boxes.map((box) => ({ ...box, collapsed })),
    );
    if (selectedStepId && !hasStep(next.sequence, selectedStepId)) setSelectedStepId(null);
    setDefinition(wrapDefinition(next));
  }

  return (
    <>
      {routeSteps.length > 1 && (
        <div className="canvas-toolbar">
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => setAllCollapsed(!allCollapsed)}
          >
            {allCollapsed ? "Expand all routes" : "Collapse all routes"}
          </button>
          <span className="muted">{routeSteps.length} routes</span>
        </div>
      )}
    <div className="workflow-designer-canvas">
      <SequentialWorkflowDesigner
        theme="light"
        undoStackSize={20}
        definition={definition}
        onDefinitionChange={handleDefinitionChange}
        selectedStepId={selectedStepId}
        onSelectedStepIdChanged={setSelectedStepId}
        toolboxConfiguration={toolboxConfiguration}
        stepsConfiguration={stepsConfiguration}
        validatorConfiguration={validatorConfiguration}
        controlBar={true}
        contextMenu={true}
        isToolboxCollapsed={false}
        isEditorCollapsed={false}
        extensions={ROUTE_TOGGLE_EXTENSIONS}
        customActionHandler={toggleRouteHandler}
        rootEditor={
          <EditorHost client={queryClient}>
            <WorkflowRootEditor />
          </EditorHost>
        }
        stepEditor={
          <EditorHost client={queryClient}>
            <WorkflowStepEditor catalog={catalog} safeToKey={safeToKey} />
          </EditorHost>
        }
      />
    </div>
    </>
  );
}

function WorkflowRootEditor() {
  return (
    <div className="designer-root-editor">
      <span className="stage-kicker">How to build</span>
      <ol className="designer-howto">
        <li>Drag a <b>trigger</b> onto the canvas (only one).</li>
        <li>Click a <b>Condition</b> box to filter files. Leave it empty to run for every file.</li>
        <li>Drag <b>actions</b> into a Condition box. Add as many as you need.</li>
        <li>
          Need different handling for different files? Drag in another <b>Condition</b>: each one
          is its own route with its own actions.
        </li>
      </ol>
      <p className="muted">Click any step to edit its settings.</p>
    </div>
  );
}

function WorkflowStepEditor({
  catalog,
  safeToKey,
}: {
  catalog: ComponentType[];
  safeToKey: Map<string, string>;
}) {
  const { name, setName, type, componentType, properties, setProperty, isReadonly, step } =
    useStepEditor<WorkflowStep, WorkflowDefinition>();

const props = properties as Record<string, unknown>;
  if (props.role === "condition") {
    // The same editor serves an open Condition box and its collapsed node.
    const collapsed = componentType !== "container";
    const kept = Array.isArray(props.components) ? props.components.length : 0;
    const conditionType = catalog.find((t) => t.key === CONDITION_KEY);
    const config = isRecord(props.config) ? props.config : DEFAULT_CONDITION;
    return (
      <div className="designer-step-editor">
        <div className="designer-step-meta">
          <span className="stage-kicker">Condition</span>
        </div>
        <p className="muted">
          Only files that match go into this box, and everything inside it then runs from top to
          bottom. Leave every filter empty to run for every file. A file is checked against every
          Condition box on the canvas, so it can follow more than one route.
        </p>
        <div className="schema-field schema-check">
          <label>
            <input
              type="checkbox"
              checked={collapsed}
              disabled={isReadonly}
              onChange={(e) => setProperty("collapsed", e.target.checked)}
            />{" "}
            Collapse this route
          </label>
          <p className="field-help">
            Hides the steps to keep the canvas tidy. They are kept and still run; untick to show them
            again.
          </p>
        </div>
        <SchemaForm
          schema={conditionType?.config_schema ?? { type: "object" }}
          value={config}
          disabled={isReadonly}
          onChange={(next) => {
            setProperty("config", next);
            setName(boxName(next, collapsed ? kept : undefined));
          }}
        />
      </div>
    );
  }
  if (componentType === "container") {
    
    const group = GROUPS.find((g) => g.key === props.group);
    const parallel = props.parallel === true;
    const count = ((step as unknown as BoxStep).sequence ?? []).length;
    return (
      <div className="designer-step-editor">
        <div className="designer-step-meta">
          <span className="stage-kicker">{group?.title ?? "Group"}</span>
        </div>
        <p className="muted">{groupHelp(group?.key)}</p>
        <div className="schema-field schema-check">
          <label>
            <input
              type="checkbox"
              checked={parallel}
              disabled={isReadonly}
              onChange={(e) => {
                setProperty("parallel", e.target.checked);
                setName(groupTitle(group?.title ?? "Group", e.target.checked, count));
              }}
            />{" "}
            Run all steps in this box at the same time
          </label>
          <p className="field-help">
            Faster. The steps are independent: if one fails the others still finish, and only the
            failed ones are retried. Turn this off to run them one by one, top to bottom, stopping at
            the first failure.
          </p>
        </div>
      </div>
    );
  }

  const typeKey =
    typeof properties.typeKey === "string" ? properties.typeKey : safeToKey.get(type);
  const catalogType = catalog.find((t) => t.key === typeKey);
  const config = isRecord(properties.config) ? properties.config : {};
  const enabled = properties.enabled !== false;
  const kind = typeKey ? uiName(typeKey, catalogType?.name ?? type) : type;

  return (
    <div className="designer-step-editor">
      <div className="designer-step-meta">
        <span className="stage-kicker">{kind}</span>
      </div>
      <div className="field">
        <label htmlFor="designer-step-name">Name</label>
        <input
          id="designer-step-name"
          value={name}
          disabled={isReadonly}
          placeholder={labelPlaceholder(typeKey)}
          onChange={(e) => setName(e.target.value)}
        />
        <p className="field-help">Shown on the canvas so you can recognise this step.</p>
      </div>
      <div className="schema-field schema-check">
        <label>
          <input
            type="checkbox"
            checked={enabled}
            disabled={isReadonly}
            onChange={(e) => setProperty("enabled", e.target.checked)}
          />{" "}
          Enabled
        </label>
      </div>
      <SchemaForm
        schema={catalogType?.config_schema ?? { type: "object" }}
        value={config}
        disabled={isReadonly}
        onChange={(next) => setProperty("config", next)}
      />
    </div>
  );
}

function groupHelp(key?: string): string {
  switch (key) {
    case "output.file.copy":
      return "Each “Copy to folder” inside this box copies the file to one destination (Folder A, Folder B, …). Drag more from the toolbox to add destinations.";
    case "output.notify.webhook":
      return "Each “Webhook / API call” inside this box sends one HTTP request (a notification, an API call, …). Drag more from the toolbox to add calls.";
    case "output.notify.email":
      return "Each “Send email” inside this box sends one email. Drag more from the toolbox to add emails.";
    default:
      return "Drag steps from the toolbox into this box.";
  }
}

function labelPlaceholder(typeKey?: string): string {
  switch (typeKey) {
    case "output.file.copy":
      return "e.g. Folder A";
    case "output.notify.webhook":
      return "e.g. Notification A";
    case "output.notify.email":
      return "e.g. Email finance";
    default:
      return "";
  }
}

// ---- flat components -> canvas tree -------------------------------------

// One Condition box on the canvas. `route` is "" for the default box (id
// "condition-box"); every other box uses its own id as its route.
interface RouteBox {
  route: string;
  config: Record<string, unknown>;
  typeVersion: number;
  /** Shown as one compact node; its steps are kept inside the node. */
  collapsed?: boolean;
}

function routeOf(component: EditableComponent): string {
  return component.route ?? "";
}

function toDefinition(components: EditableComponent[], boxes?: RouteBox[]): WorkflowDefinition {
  const triggers = components.filter((c) => c.stage === "trigger");
  const body = [
    ...components.filter((c) => c.stage === "process"),
    ...components.filter((c) => c.stage === "output"),
  ];

  // Boxes keep the order they have on the canvas, so empty ones survive; any
  // route that only exists in the data is added after them.
  const order: RouteBox[] = [...(boxes ?? [])];
  for (const component of body) {
    if (!order.some((box) => box.route === routeOf(component))) {
      order.push({ route: routeOf(component), config: DEFAULT_CONDITION, typeVersion: 1 });
    }
  }
  if (order.length === 0) order.push({ route: "", config: DEFAULT_CONDITION, typeVersion: 1 });

  const routeBoxes = order.map((box) =>
    buildRouteBox(box, body.filter((c) => routeOf(c) === box.route)),
  );
  return { properties: {}, sequence: [...triggers.map(toStep), ...routeBoxes] };
}

function buildRouteBox(box: RouteBox, items: EditableComponent[]): Step {
  const boxId = box.route === "" ? CONDITION_BOX_ID : box.route;
  const processes = items.filter((c) => c.stage === "process");
  const outputs = items.filter((c) => c.stage === "output");

  // The first process step of a route becomes its Condition if it is one.
  let condition: EditableComponent | undefined;
  let steps = processes;
  if (processes[0]?.type === CONDITION_KEY) {
    condition = processes[0];
    steps = processes.slice(1);
  }

  if (box.collapsed) {
    // One compact node. It keeps the route's steps in its properties so that
    // expanding it brings back exactly what was there.
    const kept = [...steps, ...outputs];
    const collapsedConfig = condition ? condition.config : box.config;
    return {
      id: boxId,
      componentType: "task",
      type: "conditionboxcollapsed",
      name: boxName(collapsedConfig, kept.length),
      properties: {
        role: "condition",
        collapsed: true,
        typeVersion: condition?.type_version ?? box.typeVersion,
        config: collapsedConfig,
        components: kept,
      },
    };
  }

  const children: Step[] = steps.map(toStep);
  const grouped = new Set<string>(GROUPS.map((g) => g.key));
  for (const group of GROUPS) {
    const members = outputs.filter((c) => c.type === group.key);
    if (members.length === 0) continue;
    // A box runs at the same time only when every member in it is set to.
    const parallel = members.every((member) => Boolean(member.parallel_group));
    const groupBox: BoxStep = {
      id: group.id + "-" + boxId,
      componentType: "container",
      type: group.type,
      name: groupTitle(group.title, parallel, members.length),
      properties: { role: "group", group: group.key, parallel },
      sequence: members.map(toStep),
    };
    children.push(groupBox);
  }
  for (const other of outputs.filter((c) => !grouped.has(c.type))) {
    children.push(toStep(other));
  }

  const config = condition ? condition.config : box.config;
  const routeBox: BoxStep = {
    id: boxId,
    componentType: "container",
    type: "conditionbox",
    name: conditionTitle(config),
    properties: {
      role: "condition",
      typeVersion: condition?.type_version ?? box.typeVersion,
      config,
    },
    sequence: children,
  };
  return routeBox;
}

function toStep(component: EditableComponent): WorkflowStep {
  return {
    id: component.clientId,
    componentType: "task",
    type: safeType(component.type),
    name: component.label || component.type,
    properties: {
      stage: component.stage,
      typeKey: component.type,
      typeVersion: component.type_version ?? 1,
      config: component.config,
      enabled: component.enabled,
      parallelGroup: component.parallel_group ?? "",
    },
  };
}

// ---- canvas tree -> flat components -------------------------------------

interface Extracted {
  components: EditableComponent[];
  boxes: RouteBox[];
}

function fromDefinition(
  definition: WorkflowDefinition,
  catalogByKey: Map<string, ComponentType>,
  safeToKey: Map<string, string>,
): Extracted {
  type Bucket = {
    condition: EditableComponent | null;
    processes: EditableComponent[];
    outputs: EditableComponent[];
  };
  const triggers: EditableComponent[] = [];
  const boxes: RouteBox[] = [];
  const loose: EditableComponent[] = [];
  const seenIds = new Set<string>();
  const byRoute = new Map<string, Bucket>();
  const bucket = (route: string): Bucket => {
    let b = byRoute.get(route);
    if (!b) {
      b = { condition: null, processes: [], outputs: [] };
      byRoute.set(route, b);
    }
    return b;
  };

  // route: the enclosing Condition box (null = outside every box).
  // inherited: an action box decides whether its steps run at the same time
  // ("" = one by one).
  const visit = (steps: Step[], route: string | null, inherited?: { key: string }) => {
    for (const step of steps) {
      if (step.componentType !== "container" && step.properties.role === "condition") {
        // A collapsed route: its steps are stored inside the node.
        const own = step.id === CONDITION_BOX_ID ? "" : step.id;
        boxes.push({
          route: own,
          config: isRecord(step.properties.config) ? step.properties.config : DEFAULT_CONDITION,
          typeVersion:
            typeof step.properties.typeVersion === "number" ? step.properties.typeVersion : 1,
          collapsed: step.properties.collapsed !== false,
        });
        bucket(own).condition = conditionFromBox(step, own);
        const stored = Array.isArray(step.properties.components)
          ? (step.properties.components as unknown as EditableComponent[])
          : [];
        for (const original of stored) {
          // A duplicated route copies its stored steps; give the copies new ids.
          const id = seenIds.has(original.clientId)
            ? original.clientId + "-" + Math.random().toString(36).slice(2, 8)
            : original.clientId;
          seenIds.add(id);
          const component: EditableComponent = { ...original, clientId: id, route: own };
          (component.stage === "process" ? bucket(own).processes : bucket(own).outputs).push(component);
        }
        continue;
      }
      if (step.componentType === "container") {
        const children = (step as BoxStep).sequence ?? [];
        if (step.properties.role === "condition") {
          const own = step.id === CONDITION_BOX_ID ? "" : step.id;
          boxes.push({
            route: own,
            config: isRecord(step.properties.config) ? step.properties.config : DEFAULT_CONDITION,
            typeVersion:
              typeof step.properties.typeVersion === "number" ? step.properties.typeVersion : 1,
            collapsed: step.properties.collapsed === true,
          });
          bucket(own).condition = conditionFromBox(step, own);
          visit(children, own);
        } else if (step.properties.role === "group") {
          visit(children, route, {
            key: step.properties.parallel === true ? String(step.properties.group ?? "") : "",
          });
        } else {
          visit(children, route, inherited);
        }
        continue;
      }
      const component = componentFromStep(step as WorkflowStep, catalogByKey, safeToKey);
      if (!component) continue;
      seenIds.add(component.clientId);
      if (inherited) component.parallel_group = inherited.key;
      if (component.stage === "trigger") {
        triggers.push(component);
      } else if (route === null) {
        loose.push(component);
      } else {
        component.route = route;
        (component.stage === "process" ? bucket(route).processes : bucket(route).outputs).push(component);
      }
    }
  };
  visit(definition.sequence, null);

  // Steps dropped outside every box join the first route.
  const first = boxes[0]?.route ?? "";
  for (const component of loose) {
    component.route = first;
    (component.stage === "process" ? bucket(first).processes : bucket(first).outputs).push(component);
  }

  const routeOrder = [...boxes.map((b) => b.route)];
  for (const route of byRoute.keys()) {
    if (!routeOrder.includes(route)) routeOrder.push(route);
  }
  const components: EditableComponent[] = [...triggers];
  for (const route of routeOrder) {
    const b = byRoute.get(route);
    if (!b) continue;
    // Always put a route's condition first among its process steps.
    components.push(...(b.condition ? [b.condition] : []), ...b.processes, ...b.outputs);
  }
  return { components, boxes };
}

function conditionFromBox(step: Step, route: string): EditableComponent | null {
  const config = isRecord(step.properties.config) ? step.properties.config : DEFAULT_CONDITION;
  if (isTrivialCondition(config)) return null;
  return {
    clientId: CONDITION_COMPONENT_ID + "-" + (route || "default"),
    label: "Condition",
    stage: "process",
    type: CONDITION_KEY,
    type_version:
      typeof step.properties.typeVersion === "number" ? step.properties.typeVersion : 1,
    config,
    enabled: true,
    route,
  };
}

function componentFromStep(
  step: WorkflowStep,
  catalogByKey: Map<string, ComponentType>,
  safeToKey: Map<string, string>,
): EditableComponent | null {
  const typeKey =
    typeof step.properties.typeKey === "string"
      ? step.properties.typeKey
      : safeToKey.get(step.type);
  const type = typeKey ? catalogByKey.get(typeKey) : undefined;
  if (!type) return null;
  const stage = isStage(step.properties.stage) ? step.properties.stage : type.stages[0];
  if (!stage) return null;
  return {
    clientId: step.id,
    label: step.name,
    stage,
    type: type.key,
    type_version:
      typeof step.properties.typeVersion === "number"
        ? step.properties.typeVersion
        : type.version,
    config: isRecord(step.properties.config) ? step.properties.config : {},
    enabled: step.properties.enabled !== false,
    parallel_group:
      stage === "output" && typeof step.properties.parallelGroup === "string"
        ? step.properties.parallelGroup
        : "",
    route: "",
  };
}

// A box is either fully parallel or not: make every member agree, and clear the
// setting on anything that is not an output step. Each route is judged on its own.
function normalizeParallel(components: EditableComponent[]): EditableComponent[] {
  const slot = (c: EditableComponent) => routeOf(c) + "|" + c.type;
  const members = new Map<string, EditableComponent[]>();
  for (const c of components) {
    if (c.stage !== "output" || !GROUPS.some((g) => g.key === c.type)) continue;
    members.set(slot(c), [...(members.get(slot(c)) ?? []), c]);
  }
  const parallelSlots = new Set<string>();
  for (const [key, list] of members) {
    if (list.every((c) => Boolean(c.parallel_group))) parallelSlots.add(key);
  }
  return components.map((component) => {
    const next = component.stage === "output" && parallelSlots.has(slot(component)) ? component.type : "";
    return component.parallel_group === next ? component : { ...component, parallel_group: next };
  });
}


// ---- toolbox -----------------------------------------------------------

function buildToolbox(
  catalog: ComponentType[],
  catalogByKey: Map<string, ComponentType>,
  safeToKey: Map<string, string>,
  hasTrigger: boolean,
): ToolboxConfiguration {
  const groups: ToolboxConfiguration["groups"] = [];
  const triggers = catalog.filter((t) => t.stages.includes("trigger"));
  const prepare = catalog.filter((t) => t.stages.includes("process") && t.key !== CONDITION_KEY);
  const actions = catalog.filter((t) => t.stages.includes("output"));

  if (!hasTrigger && triggers.length > 0) {
    groups.push({ name: "1 · Trigger", steps: triggers.map((t) => toToolboxStep(t, "trigger")) });
  }
  groups.push({ name: "2 · Condition", steps: [conditionToolboxStep()] });
  if (prepare.length > 0) {
    groups.push({ name: "3 · Prepare file", steps: prepare.map((t) => toToolboxStep(t, "process")) });
  }
  if (actions.length > 0) {
    groups.push({ name: "4 · Actions", steps: actions.map((t) => toToolboxStep(t, "output")) });
  }

  return {
    labelProvider: (step) => step.name,
    descriptionProvider: (step) =>
      step.type === "conditionbox"
        ? "A new route: its own filter and its own actions. Files that match run it; others skip it."
        : (catalogByKey.get(safeToKey.get(step.type) ?? "")?.description ?? ""),
    groups,
  };
}

// A new, empty route. Dropping it adds another Condition box to the canvas.
function conditionToolboxStep(): Omit<Step, "id"> {
  return {
    componentType: "container",
    type: "conditionbox",
    name: conditionTitle(DEFAULT_CONDITION),
    properties: { role: "condition", typeVersion: 1, config: cloneConfig(DEFAULT_CONDITION) },
    sequence: [],
  } as unknown as Omit<Step, "id">;
}

function toToolboxStep(type: ComponentType, stage: WorkflowStage): Omit<Step, "id"> {
  return {
    componentType: "task",
    type: safeType(type.key),
    name: uiName(type.key, type.name),
    properties: {
      stage,
      typeKey: type.key,
      typeVersion: type.version,
      config: cloneConfig(type.default_config),
      enabled: true,
      // New actions join their box running at the same time by default.
      parallelGroup: stage === "output" && GROUPS.some((g) => g.key === type.key) ? type.key : "",
    },
  };
}

// ---- helpers -----------------------------------------------------------

// Steps dropped from the toolbox share a default name; number the repeats
// ("Copy to folder", "Copy to folder 2", …) until the user renames them.
function numberDuplicateNames(
  components: EditableComponent[],
  catalogByKey: Map<string, ComponentType>,
): EditableComponent[] {
  const seen = new Map<string, number>();
  return components.map((component) => {
    const base = uiName(component.type, catalogByKey.get(component.type)?.name ?? component.type);
    if (component.label !== base && !component.label.startsWith(base + " ")) {
      return component;
    }
    const count = (seen.get(base) ?? 0) + 1;
    seen.set(base, count);
    if (component.label !== base || count === 1) return component;
    return { ...component, label: base + " " + count };
  });
}

function sameStructure(a: WorkflowDefinition, b: WorkflowDefinition): boolean {
  return structureOf(a.sequence) === structureOf(b.sequence);
}

function structureOf(steps: Step[]): string {
  return steps
    .map((step) =>
      step.componentType === "container"
        ? step.id + "[" + structureOf((step as BoxStep).sequence ?? []) + "]"
        : step.id,
    )
    .join("|");
}

function hasStep(steps: Step[], id: string): boolean {
  return steps.some(
    (step) =>
      step.id === id ||
      (step.componentType === "container" && hasStep((step as BoxStep).sequence ?? [], id)),
  );
}

function hasValue(value: unknown): boolean {
  if (value === undefined || value === null) return false;
  if (typeof value === "string") return value.trim() !== "";
  if (Array.isArray(value)) return value.length > 0;
  return true;
}

function safeType(key: string): string {
  return key.replace(/[^A-Za-z0-9]/g, "");
}

function isStage(value: unknown): value is WorkflowStage {
  return value === "trigger" || value === "process" || value === "output";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function cloneConfig(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) return {};
  return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
}
