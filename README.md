# Videohub Control 7.0.6 (Linux)

Recréation de **Blackmagic Videohub Control 7.0.6** sous forme d'application web locale :
un petit serveur Python (aucune dépendance) parle au Videohub via le protocole Ethernet
Blackmagic (TCP 9990) et sert une interface dans le navigateur.

## Lancer

```sh
python3 -m videohub_control --open                 # http://localhost:8090
python3 -m videohub_control --hub 10.0.0.50        # connexion directe au démarrage
python3 -m videohub_control --bind 0.0.0.0         # accessible depuis le réseau (tablette…)
```

Python ≥ 3.10. Le dernier Videohub utilisé est reconnecté automatiquement au démarrage.

> `--bind 0.0.0.0` n'ajoute aucune authentification : toute personne ayant accès au port peut router.

## Fonctions

- **Panneau** : boutons Destinations / Sources. Sélectionner une destination met en évidence
  sa source ; cliquer une source route. Ctrl/Maj+clic pour router plusieurs destinations d'un coup.
- **Mode Take** : la source est armée (clignote) puis validée avec TAKE ou Entrée.
- **Verrous** : verrouiller / déverrouiller / forcer le déverrouillage (O / U / F du protocole).
- **Matrice** : grille de points de croisement, en-têtes fixes, adaptée jusqu'à 288×288.
- **Étiquettes** : édition des noms d'entrées, sorties et sorties moniteur, import/export CSV.
- **Salves** : mémoriser le routage (tout ou sélection) et le rappeler en un clic ; les sorties
  verrouillées par d'autres sont ignorées. Les salves sont liées à l'ID unique du Videohub.
- **Sorties moniteur** (Videohub qui en ont), **découverte réseau** (mDNS), favoris,
  reconnexion automatique, synchronisation temps réel entre plusieurs navigateurs.

Configuration : `~/.config/videohub-control/config.json`.

## Tests

```sh
python3 -m unittest discover -s tests -t .
```
