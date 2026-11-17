import {
  ClickCommandType,
  type CustomActionHandler,
  type DesignerExtension,
  type StepComponentView,
  type StepContext,
} from "sequential-workflow-designer";

/*
 * A small "−" / "+" button on every Condition box.
 *
 * The free designer has no collapse control on containers, so this uses its
 * extension points: the view wrapper draws the button and turns a click on it
 * into a custom action, and the handler flips the route's "collapsed" property.
 * WorkflowDesigner then rebuilds the canvas from that property, exactly as it
 * does for the "Collapse this route" checkbox in the editor panel.
 */

export const TOGGLE_ROUTE_ACTION = "toggleRoute";

const SVG_NS = "http://www.w3.org/2000/svg";
const AMBER = "#a97400";

function svg<K extends keyof SVGElementTagNameMap>(
  name: K,
  attributes: Record<string, string | number>,
): SVGElementTagNameMap[K] {
  const element = document.createElementNS(SVG_NS, name);
  for (const [key, value] of Object.entries(attributes)) element.setAttribute(key, String(value));
  return element;
}

// A filled circle with a white "−" (open box) or "+" (collapsed box).
function createToggleButton(cx: number, cy: number, r: number, collapsed: boolean): SVGGElement {
  const g = svg("g", { class: "sqd-route-toggle", style: "cursor: pointer" });
  const title = svg("title", {});
  title.textContent = collapsed ? "Expand this route" : "Collapse this route";
  g.appendChild(title);
  g.appendChild(svg("circle", { cx, cy, r, fill: AMBER }));
  const arm = r * 0.5;
  const d = collapsed
    ? "M" + (cx - arm) + " " + cy + "H" + (cx + arm) + "M" + cx + " " + (cy - arm) + "V" + (cy + arm)
    : "M" + (cx - arm) + " " + cy + "H" + (cx + arm);
  g.appendChild(
    svg("path", { d, fill: "none", stroke: "#fff", "stroke-width": 2, "stroke-linecap": "round" }),
  );
  return g;
}

export const routeToggleExtension: DesignerExtension = {
  stepComponentViewWrapper: {
    wrap(view: StepComponentView, context: StepContext): StepComponentView {
      const step = context.step;
      if (context.isPreview || step.properties.role !== "condition") return view;

      const collapsed = step.componentType !== "container";
      let button: SVGGElement;
      if (collapsed) {
        // Sit exactly on the node's icon so the "+" replaces it.
        const icon = view.g.querySelector("image, .sqd-step-task-empty-icon");
        const x = Number(icon?.getAttribute("x") ?? 12);
        const y = Number(icon?.getAttribute("y") ?? 10);
        const size = Number(icon?.getAttribute("width") ?? 22);
        button = createToggleButton(x + size / 2, y + size / 2, size / 2, true);
      } else {
        // Top-left corner of the open box.
        button = createToggleButton(15, 15, 8, false);
      }
      view.g.appendChild(button);

      const resolveClick = view.resolveClick.bind(view);
      view.resolveClick = (click) => {
        if (button.contains(click.element)) {
          return {
            type: ClickCommandType.triggerCustomAction,
            step,
            sequence: context.parentSequence,
            action: { type: TOGGLE_ROUTE_ACTION },
          };
        }
        return resolveClick(click);
      };
      return view;
    },
  },
};

export const toggleRouteHandler: CustomActionHandler = (action, step, _sequence, context) => {
  if (action.type !== TOGGLE_ROUTE_ACTION || !step) return;
  // An open box (container) collapses; a collapsed node (task) expands.
  step.properties.collapsed = step.componentType === "container";
  context.notifyStepPropertiesChanged(step.id);
};
