# paraphe — outiller la recherche de parrainages d'élus

Pour se présenter à l'élection présidentielle française, il faut réunir
**500 présentations d'élus** (« parrainages »), venues d'au moins
30 départements, avec un plafond de 50 par département. Depuis 2017, chaque
présentation est **publiée nominativement**. Beaucoup de maires n'osent plus
signer, et des candidatures sérieuses s'arrêtent avant que le débat ait lieu.

Ce dépôt contient l'outillage complet d'une campagne de recherche de
parrainages : croisement de données publiques pour identifier les élus les
plus susceptibles d'écouter, génération de messages personnalisés, et une
application web pour qu'une équipe de bénévoles se répartisse le travail sans
se marcher dessus.

**Il est indépendant de tout candidat.** Le nom du candidat, les textes et les
coordonnées sont des paramètres : la même base sert n'importe quelle
candidature, de n'importe quelle sensibilité. C'est un outil de pluralisme,
pas de campagne pour quelqu'un en particulier — voir [DEMARCHE.md](DEMARCHE.md).

## Ce que ça fait

1. **Identifier** — croise trois bases ouvertes (parrainages publiés par le
   Conseil constitutionnel 2017 et 2022, Répertoire national des élus,
   annuaire de l'administration) pour produire la liste des maires
   **toujours en poste** qui ont déjà parrainé une candidature peu médiatisée,
   avec les coordonnées de leur mairie (email, téléphone, horaires, adresse).
2. **Écrire** — génère un email, une lettre imprimable et un déroulé d'appel
   personnalisés pour chaque élu, à partir de modèles éditables.
3. **Organiser** — une application web où les bénévoles réservent des lots,
   suivent les statuts, partagent leurs notes, en équipes locales cloisonnées.
   Sans état, adossée à PostgreSQL : elle tourne en plusieurs instances, et
   un chart Helm la déploie sur Kubernetes avec CloudNativePG.

## Deux versions

Une seule interface (`web/`, React), deux modes. C'est la présence d'une API
derrière la page qui tranche, à l'exécution — pas une option de compilation.

| | **Équipe** (API `api/` + interface) | **Navigateur** (interface seule) |
|---|---|---|
| Pour qui | une équipe, jusqu'à quelques dizaines | une personne seule, ou 2-3 |
| Coordination | réservations, cloisonnement par équipe, notes partagées | aucune — c'est sa limite |
| Données | PostgreSQL, sur votre serveur | **rien ne sort du navigateur** |
| Installation | Docker ou Kubernetes | une page web, rien à installer |
| Liste des élus | embarquée à la construction | publiée à côté de l'app |

La version navigateur ne coordonne rien : elle ignore si un autre bénévole a
déjà appelé le même maire. C'est le prix de la confidentialité maximale, et
l'interface le dit. Pour une campagne à plusieurs dizaines de personnes, la
version serveur évite les doublons — qui sont le pire faux pas possible
vis-à-vis d'un élu.

La version navigateur charge d'office la **liste prioritaire** (1 972 maires,
139 Ko) et propose la **base complète** (34 826 maires, 2 Mo) d'un clic, avec
une barre de progression. Vous pouvez aussi charger votre propre fichier :
les listes publiées sont une commodité, elles vieillissent, et `task all` les
régénère à jour.

## Démarrer

```bash
devbox install && task
```

`task` seul liste toutes les commandes. Les principales :

| Commande | Effet |
|---|---|
| `task all` | télécharge les sources ouvertes et construit toutes les listes |
| `task messages` | génère le publipostage (emails CSV, lettres HTML) |
| `task build` | recroise les sources sans les retélécharger |
| `task db` | démarre PostgreSQL en local (requis par `task api`) |
| `task api` | lance l'API d'équipe sur http://127.0.0.1:8047 |
| `task web` | l'interface en développement (http://127.0.0.1:5180) |
| `task test` | tous les tests : outils et noyau (TS), API (Go), interface (TS) |
| `task deploy` | construit et lance l'app en Docker |

Avant toute génération, remplissez `config/campagne.toml` (identité du
candidat, coordonnées, signature) — sinon les scripts refusent de tourner
plutôt que d'expédier « Prénom NOM » à des milliers d'élus.

Le déploiement sur un serveur, les comptes et le cloisonnement par équipe sont
décrits dans [DEPLOIEMENT.md](DEPLOIEMENT.md). Le mode d'emploi destiné aux
bénévoles est dans [GUIDE.md](GUIDE.md), et l'application le sert elle-même.

## Comment c'est fait

**Deux langages, pas trois.** Go pour le serveur, TypeScript pour tout le
reste :

- `noyau/` — **TypeScript**, sans dépendance : le moteur de messages, la
  lecture/écriture CSV, la normalisation des noms. Une seule implémentation,
  partagée par l'interface et par les outils.
- `outils/` — **TypeScript exécuté par Node**, sans compilation
  (`node outils/build.ts`) : le croisement des sources ouvertes et le
  publipostage de masse.
- `api/` — **Go** : l'API JSON de l'application d'équipe (une image de 30 Mo
  avec l'interface dedans, sans état, plusieurs instances devant la même
  base). Elle ne rend aucun HTML et ne génère aucun message.
- `web/` — **React + Vite** : l'interface, dans ses deux modes.
- `outils/telecharger.sh` — du bash, parce que télécharger quatre fichiers ne
  mérite pas mieux.

Le moteur de messages n'existe donc **qu'une fois**. Il en a existé deux (une
copie Python pour le publipostage, une copie JavaScript pour l'interface) :
deux occasions de casser le même invariant, à corriger deux fois.

Le croisement a d'abord été écrit en Python. Sa réécriture a été validée en
comparant les quatre CSV produits **octet pour octet** sur les 34 826 lignes
réelles, y compris le port de `difflib.SequenceMatcher` dont dépend le
rapprochement des communes — une autre distance aurait silencieusement changé
la liste des maires ciblés.

## Sources de données

Toutes ouvertes, récupérées automatiquement par `task download` :

- **Parrainages 2017 et 2022** — Conseil constitutionnel via data.gouv.fr
  (domaine public). Publication finale nominative : élu, mandat, commune,
  candidat présenté.
- **Répertoire national des élus** — ministère de l'Intérieur, fichier des
  maires, mis à jour en continu (Licence Ouverte / Etalab 2.0).
- **Annuaire de l'administration** — DILA, `api-lannuaire.service-public.fr` :
  coordonnées officielles de chaque mairie, clé = code INSEE.

Aucun scraping, aucune donnée personnelle collectée ailleurs que dans ces
publications officielles. Les coordonnées utilisées sont celles **de la
mairie**, jamais celles d'une personne privée.

Les données brutes et les listes produites **ne sont pas versionnées** : elles
se régénèrent en une commande, et un fichier figé vieillit (le Répertoire
national des élus et l'annuaire évoluent en continu). La CI les reconstruit à
chaque publication et les sert à côté de la version navigateur, avec un
`robots.txt` qui en décourage le moissonnage : ces coordonnées sont publiques
et faites pour être utilisées, mais un annuaire indexé deviendrait une cible
pour le démarchage commercial, ce qui nuirait aux élus comme à la démarche.

## Ce qui est délicat, et comment c'est traité

Croiser des fichiers d'état civil est un exercice à faux positifs, et ici un
faux positif a un coût réel : écrire « merci pour votre parrainage » à
quelqu'un qui n'a jamais signé décrédibilise la démarche entière.

- L'identité d'un parrain est **commune + nom + prénom**, jamais le nom seul :
  les successions familiales à la mairie sont fréquentes.
- Le **code sexe** du Répertoire national des élus tranche les cas que
  l'orthographe confond (Christian → Christine).
- Ce qui n'est pas certain part dans un fichier « à vérifier à la main »
  plutôt que d'être affirmé dans un sens ou dans l'autre.
- **Le message dépend de ce qu'on sait** : un élu sans historique de
  parrainage reçoit un texte qui ne lui prête rien.

Le détail, avec les cas réels qui ont motivé chaque garde-fou, est dans
`CLAUDE.md` (§ pièges payés) et dans le rapport produit par `task build`.

## Licence

MIT — voir [LICENSE](LICENSE). Reprenez, adaptez, forkez pour votre propre
campagne : c'est le but.
