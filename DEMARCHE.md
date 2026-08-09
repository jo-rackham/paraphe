# La démarche

## Le problème

Le filtre des 500 parrainages n'a pas été conçu pour être ce qu'il est devenu.
Il devait écarter les candidatures fantaisistes ; depuis que **toutes** les
présentations sont publiées nominativement (2016), il écarte surtout celles qui n'ont pas
d'appareil politique local. Un maire de village qui présente un candidat
minoritaire s'expose à son conseil municipal, à ses administrés, parfois à sa
majorité. Beaucoup renoncent — non par hostilité, mais parce que personne ne
leur a expliqué que **présenter n'est pas soutenir**, et qu'ils sont seuls
face à ce choix.

Résultat : à chaque échéance, des candidatures qui représentent des courants
réels s'arrêtent au seuil, pendant que les appareils installés réunissent
leurs signatures sans effort. Ce n'est pas un problème partisan. C'est un
problème de pluralisme, et il se pose identiquement à droite, à gauche et
ailleurs.

## Le pari

Les élus qui ont **déjà** parrainé une candidature peu médiatisée, en sachant
que ce serait public, ont démontré quelque chose qu'aucun sondage ne dit :
qu'ils distinguent présenter de soutenir, et qu'ils l'assument. Ils sont
1 972 maires encore en poste. C'est peu à l'échelle des 34 826 maires de France, et
c'est énorme comparé aux 500 signatures nécessaires.

L'outillage part de là : commencer par ceux qui ont déjà fait le geste, leur
dire d'abord merci, et n'élargir qu'ensuite — avec un message qui change selon
ce qu'on sait d'eux.

## Les principes qui ont guidé le code

**On n'affirme que ce qui est établi.** Un croisement de fichiers produit des
quasi-certitudes ; les traiter comme des certitudes conduit à remercier
quelqu'un pour un geste qu'il n'a pas fait. Tout ce qui est douteux part dans
un fichier « à vérifier à la main » — et le message envoyé dépend du rang
auquel appartient le destinataire. Un élu dont on ne sait rien reçoit un texte
qui ne lui prête rien.

**On ne présume pas des convictions.** Le projet tague les élus ayant parrainé
une candidature portant sur le fonctionnement démocratique. Ce tag qualifie un
**acte public constaté**, jamais une opinion : on ne sait pas pourquoi
quelqu'un a signé, et prétendre le savoir serait à la fois faux et
irrespectueux.

**Les données publiques restent à leur place.** On n'utilise que les
coordonnées officielles des mairies, publiées pour être utilisées. Jamais un
numéro personnel, même trouvable. Un refus est définitif et enregistré comme
tel.

Les listes sont republiées à côté de l'outil, pour qu'une équipe sans
compétence technique puisse s'en servir. C'est un arbitrage assumé : elles ne
contiennent que ce que l'État publie déjà, mais les rassembler en un fichier
prêt à l'emploi abaisse la barrière pour tout le monde — y compris pour qui
n'a rien à voir avec une campagne. D'où le `robots.txt`, la note de
provenance qui accompagne les fichiers, et le rappel qu'un élu qui demande à
ne plus être contacté doit l'être immédiatement.

**L'échelle ne dispense pas du soin.** Le publipostage de masse est limité au
vivier prioritaire ; le reste se fait à la main, une fiche à la fois, avec une
consigne de volume par personne et par jour (voir le guide de l'équipe). Pas parce que c'est plus efficace,
mais parce qu'un message d'un citoyen réel n'est pas un mailing, et qu'un élu
sait faire la différence.

**Le cloisonnement protège les personnes.** Les notes des bénévoles — qui
hésite, qui a refusé, ce qu'un maire a confié au téléphone — ne sont visibles
que dans l'équipe qui les a écrites. Les compteurs de campagne sont partagés,
sans noms.

## Pourquoi c'est public

Parce que l'asymétrie que ce code corrige n'est pas propre à une candidature.
N'importe quelle équipe, de n'importe quelle sensibilité, peut cloner ce dépôt,
mettre son candidat dans un fichier de configuration et travailler. Un outil
qui ne servirait qu'un camp reproduirait le déséquilibre qu'il prétend
corriger.

Et parce que la méthode mérite d'être discutée : la classification des
candidats en « peu médiatisés » est un choix éditorial, visible et modifiable
dans `outils/build.ts`, dont le rapport de build liste chaque candidat avec
son total et sa classe. Si ce choix vous paraît contestable, il est à un
endroit précis du code, et vous pouvez le changer.

## Ce que l'outil ne fait pas

Il ne remplit aucun formulaire à la place de qui que ce soit. Le formulaire
officiel du Conseil constitutionnel est adressé à l'élu, et lui seul le
renvoie. L'outillage s'arrête à la conversation.

Il ne prédit pas qui signera. Il classe par vraisemblance à partir d'actes
passés, ce qui est très différent, et il se trompe souvent — c'est pour ça
qu'un humain lit chaque fiche avant d'écrire.
