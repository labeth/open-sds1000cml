#!/usr/bin/env bash
set -euo pipefail
model_dir=$(cd -- "$(dirname -- "$0")" && pwd)
tool_dir=${ENGINEERING_MODEL_GO_DIR:-"$model_dir/../../../engineering-model-go"}
cd "$tool_dir"
export GOPROXY=off
python3 "$model_dir/scripts/contracts.py"
python3 "$model_dir/scripts/verify.py"
mkdir -p "$model_dir/generated/views"
log="$model_dir/generated/generation-diagnostics.log"
: > "$log"
go run ./cmd/engdoc --model "$model_dir/engmod.yml" --requirements "$model_dir/model/requirements.yml" --design "$model_dir/model/views.yml" --out "$model_dir/generated/ARCHITECTURE.adoc" --decisions-out "$model_dir/generated/DECISIONS.adoc" 2>> "$log"
python3 "$model_dir/scripts/check_go_publication.py"
python3 "$model_dir/scripts/check_rtl_publication.py"
python3 "$model_dir/scripts/check_eye_publication.py"
python3 "$model_dir/scripts/check_superres_numerical_publication.py"
python3 "$model_dir/scripts/check_superres_core_publication.py"
python3 "$model_dir/scripts/check_final_node_publication.py"
go run ./cmd/engview --model "$model_dir/engmod.yml" --out-dir "$model_dir/generated/views" 2>> "$log"
python3 "$model_dir/scripts/check_views.py"
python3 "$model_dir/scripts/check_context_views.py"
python3 "$model_dir/scripts/check_publication_views.py"
go run ./cmd/engsysml --model "$model_dir/engmod.yml" --out "$model_dir/generated/ARCHITECTURE.sysml" 2>> "$log"
go run ./cmd/engstruct --model "$model_dir/engmod.yml" --out "$model_dir/generated/STRUCTURIZR.dsl" 2>> "$log"
go run ./cmd/engtrace --model "$model_dir/engmod.yml" --requirements "$model_dir/model/requirements.yml" --format json --out "$model_dir/generated/TRACE-MATRIX.json" 2>> "$log"
python3 "$model_dir/scripts/check_diagram_coordinates.py"
python3 "$model_dir/scripts/check_coverage_panels.py"
go run ./cmd/engtrlc --requirements "$model_dir/model/requirements.yml" --out-dir "$model_dir/generated/trlc" --package SDSRequirements 2>> "$log"
printf 'Generated model publications. Diagnostics: %s\n' "$log"
