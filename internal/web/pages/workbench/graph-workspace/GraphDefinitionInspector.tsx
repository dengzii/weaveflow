import { useState } from "react";
import { Input } from "../../../components/ui/input";
import { Textarea } from "../../../components/ui/textarea";
import type {
  GraphDefinition,
  InitialStateRequirements,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
  ToolDefinition,
} from "../../../types";
import { StatusText } from "../shared";
import { CollapsibleInspectorBlock, Field, InspectorBlock } from "./shared";
import { RuntimeSettingsEditor } from "./GraphSettingsEditor";
import { InitialStateRequirementList } from "./InitialStateRequirementList";
import { RunInputEditor } from "./RunInputEditor";
import { StateModulesEditor } from "./StateModulesEditor";

interface GraphDefinitionInspectorProps {
  definition: GraphDefinition | null;
  definitionText: string;
  initialRequirements: InitialStateRequirements | null;
  directInitialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  initialStateText: string;
  runtimeSettings: RuntimeSettings | null;
  registry: RegistryInfo | null;
  toolDefinitions: ToolDefinition[];
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  onChangeDefinitionText: (value: string) => void;
  onChangeGraphField: <Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) => void;
  onChangeInitialStateText: (value: string) => void;
}

export function GraphDefinitionInspector({
  definition,
  definitionText,
  initialRequirements,
  directInitialRequirements,
  initialRequirementsError,
  initialStateText,
  runtimeSettings,
  registry,
  toolDefinitions,
  onChangeRuntimeSettings,
  onChangeDefinitionText,
  onChangeGraphField,
  onChangeInitialStateText,
}: GraphDefinitionInspectorProps) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [generalOpen, setGeneralOpen] = useState(false);
  const [jsonOpen, setJSONOpen] = useState(false);
  const requiredInitialState = [
    ...(directInitialRequirements?.required ?? []),
    ...(directInitialRequirements?.unresolved ?? []),
  ];
  const hasInitialStateHints = Boolean(
    initialRequirements &&
      (initialRequirements.unresolved.length > 0 ||
        initialRequirements.provided_by_entry.length > 0 ||
        initialRequirements.provided_by_upstream.length > 0 ||
        (initialRequirements.warnings?.length ?? 0) > 0)
  );

  return (
    <>
      {requiredInitialState.length > 0 ? (
        <InspectorBlock title="Run Input">
          {hasInitialStateHints ? <InitialStateRequirementList requirements={initialRequirements} showRequired={false} /> : null}
          <RunInputEditor
            requirements={requiredInitialState}
            analysisError={initialRequirementsError}
            initialStateText={initialStateText}
            onChangeInitialStateText={onChangeInitialStateText}
          />
        </InspectorBlock>
      ) : null}

      <CollapsibleInspectorBlock title="Runtime Settings" open={settingsOpen} onOpenChange={setSettingsOpen}>
        <RuntimeSettingsEditor settings={runtimeSettings} toolDefinitions={toolDefinitions} onChangeRuntimeSettings={onChangeRuntimeSettings} />
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock title="General" open={generalOpen} onOpenChange={setGeneralOpen}>
        <Field label="Name">
          <Input
            aria-label="Name"
            value={definition?.name ?? ""}
            onChange={(event) => onChangeGraphField("name", event.target.value)}
            disabled={!definition}
          />
        </Field>

        <Field label="Description">
          <Textarea
            aria-label="Description"
            value={definition?.description ?? ""}
            onChange={(event) => onChangeGraphField("description", event.target.value)}
            disabled={!definition}
            className="h-20 text-xs"
          />
        </Field>

        <div className="grid min-w-0 gap-1">
          <span className="text-xs font-medium text-muted-foreground">State Modules</span>
          <StateModulesEditor
            definition={definition}
            registry={registry}
            onChange={(stateModules) => onChangeGraphField("state_modules", stateModules)}
          />
        </div>
      </CollapsibleInspectorBlock>

      <CollapsibleInspectorBlock title="Graph JSON" open={jsonOpen} onOpenChange={setJSONOpen}>
        <Textarea
          aria-label="Graph JSON"
          value={definitionText}
          onChange={(event) => onChangeDefinitionText(event.target.value)}
          className="h-80 resize-y font-mono text-[11px] leading-5"
          spellCheck={false}
        />
        {!definition ? <StatusText tone="danger">Invalid graph JSON</StatusText> : null}
      </CollapsibleInspectorBlock>

    </>
  );
}
