import { useState } from "react";
import { Input } from "../../../components/ui/input";
import { Textarea } from "../../../components/ui/textarea";
import type {
  GraphDefinition,
  InitialStateRequirements,
  RegistryInfo,
  RuntimeSettings,
  RuntimeSettingsUpdate,
} from "../../../types";
import { StatusText } from "../shared";
import { CollapsibleInspectorBlock, InspectorBlock } from "./shared";
import { RuntimeSettingsEditor } from "./GraphSettingsEditor";
import { InitialStateRequirementList } from "./InitialStateRequirementList";
import { RunInputEditor } from "./RunInputEditor";
import { StateModulesEditor } from "./StateModulesEditor";

interface GraphDefinitionInspectorProps {
  definition: GraphDefinition | null;
  definitionText: string;
  initialRequirements: InitialStateRequirements | null;
  initialRequirementsError: string;
  initialStateText: string;
  runtimeSettings: RuntimeSettings | null;
  registry: RegistryInfo | null;
  onChangeRuntimeSettings: (settings: RuntimeSettingsUpdate) => RuntimeSettings;
  onChangeDefinitionText: (value: string) => void;
  onChangeGraphField: <Key extends keyof GraphDefinition>(key: Key, value: GraphDefinition[Key]) => void;
  onChangeInitialStateText: (value: string) => void;
}

export function GraphDefinitionInspector({
  definition,
  definitionText,
  initialRequirements,
  initialRequirementsError,
  initialStateText,
  runtimeSettings,
  registry,
  onChangeRuntimeSettings,
  onChangeDefinitionText,
  onChangeGraphField,
  onChangeInitialStateText,
}: GraphDefinitionInspectorProps) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [jsonOpen, setJSONOpen] = useState(false);
  const requiredInitialState = initialRequirements?.required ?? [];
  const hasInitialStateHints = Boolean(
    initialRequirements &&
      (initialRequirements.unresolved.length > 0 ||
        initialRequirements.provided_by_upstream.length > 0 ||
        (initialRequirements.warnings?.length ?? 0) > 0)
  );

  return (
    <>
      <InspectorBlock title="Name">
        <Input
          aria-label="Name"
          value={definition?.name ?? ""}
          onChange={(event) => onChangeGraphField("name", event.target.value)}
          disabled={!definition}
        />
      </InspectorBlock>

      <InspectorBlock title="Description">
        <Textarea
          aria-label="Description"
          value={definition?.description ?? ""}
          onChange={(event) => onChangeGraphField("description", event.target.value)}
          disabled={!definition}
          className="h-20 text-xs"
        />
      </InspectorBlock>

      <InspectorBlock title="State Modules">
        <StateModulesEditor
          definition={definition}
          registry={registry}
          onChange={(stateModules) => onChangeGraphField("state_modules", stateModules)}
        />
      </InspectorBlock>

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

      <CollapsibleInspectorBlock title="Runtime Settings" open={settingsOpen} onOpenChange={setSettingsOpen}>
        <RuntimeSettingsEditor settings={runtimeSettings} onChangeRuntimeSettings={onChangeRuntimeSettings} />
      </CollapsibleInspectorBlock>

      <InspectorBlock title="Run Input">
        {hasInitialStateHints ? <InitialStateRequirementList requirements={initialRequirements} showRequired={false} /> : null}
        <RunInputEditor
          requirements={requiredInitialState}
          analysisError={initialRequirementsError}
          initialStateText={initialStateText}
          onChangeInitialStateText={onChangeInitialStateText}
        />
      </InspectorBlock>
    </>
  );
}
