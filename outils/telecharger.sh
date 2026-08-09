#!/usr/bin/env bash
# Downloads the 4 open sources (pinned URLs, open licence / Etalab).
# The RNE and the directory evolve: rerun to refresh before a campaign.
set -euo pipefail
# data/raw/ is not versioned: a fresh clone does not have it
mkdir -p "$(dirname "$0")/../data/raw"
cd "$(dirname "$0")/../data/raw"

# Validated endorsements, final publication of the Conseil constitutionnel (data.gouv.fr)
curl -fsSL --retry 3 --retry-all-errors -o parrainages2022.csv \
  'https://static.data.gouv.fr/resources/parrainages-des-candidats-a-lelection-presidentielle-francaise-de-2022/20220307-183308/parrainagestotal.csv'
curl -fsSL --retry 3 --retry-all-errors -o parrainages2017.csv \
  'https://static.data.gouv.fr/resources/parrainages/20170320-103202/parrainagestotal.csv'

# National register of elected officials (RNE) — mayors in office (most recent dump:
# https://www.data.gouv.fr/api/1/datasets/repertoire-national-des-elus-1/)
RNE_URL=$(curl -s 'https://www.data.gouv.fr/api/1/datasets/repertoire-national-des-elus-1/' \
  | jq -r '.resources[] | select(.title | test("maires")) | .url' | head -1)
curl -fsSL --retry 3 --retry-all-errors -o rne_maires.csv "$RNE_URL"

# Government services directory (DILA) — town hall cards with email/address/phone
curl -fsSL --retry 3 --retry-all-errors -o annuaire_mairies.csv \
  'https://api-lannuaire.service-public.fr/api/explore/v2.1/catalog/datasets/api-lannuaire-administration/exports/csv?where=pivot%20like%20%22mairie%22&select=id%2Cnom%2Cpivot%2Cadresse_courriel%2Ctelephone%2Cadresse%2Csite_internet%2Cformulaire_contact%2Ccode_insee_commune%2Cplage_ouverture'

wc -l parrainages2022.csv parrainages2017.csv rne_maires.csv annuaire_mairies.csv
