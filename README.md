# Code Review Leaderboard

A little tool which scrapes a Github organisation and summarizes user activity data in a table.

## Install
```bash
$ gh extension install ChristianMoesl/gh-leaderboard
```


## Usage
```bash
$ gh leaderboard --since 2024-03-01 --org myorg --name '^btb-.*$'
Processing data since 2024-03-01 00:00:00 +0000 UTC matching repository name pattern ^btb-.*$
btb-bingo-service ... done! [2 in 0s]
btb-papaya ... done! [2 in 813ms]
btb-mbs ... done! [2 in 1.456s]
btb-wingman ... done! [2 in 1.504s]
btb-lmnop ... done! [2 in 1.333s]
btb-racoon ... done! [2 in 1.333s]
btb-rgs ... done! [3 in 1.863s]
btb-brb-dll ... done! [3 in 1.829s]
btb-ringo-2 ... done! [3 in 1.968s]
btb-galactic ... done! [6 in 2.977s]
btb-entity-kaos-service ... done! [5 in 2.739s]
btb-omega-star ... done! [8 in 4.538s]
750 requests sent to Github
Rate Limit: remaining=3398 reset=2024-04-05 22:03:28 +0200 CEST

+---------------------------------------------------------------------------+
| Code Review Leaderboard                                                   |
+---------------------+---------------+---------+----------+----------------+
| USER                | PULL REQUESTS | REVIEWS | COMMENTS | #COMMENT LINES |
+---------------------+---------------+---------+----------+----------------+
| ChristianMoesl      |            52 |      50 |       24 |             85 |
| User1               |            67 |       0 |        0 |              0 |
| User2               |             0 |       1 |        0 |              1 |
| User3               |            41 |      97 |        9 |            107 |
+---------------------+---------------+---------+----------+----------------+
```
