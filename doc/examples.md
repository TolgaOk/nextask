# Examples

## Source Snapshots

Snapshots capture the working tree (including uncommitted changes) and push it to a git remote. Workers clone from it before running the task.

### Local bare repo

```bash
nextask init source                                                  # creates ~/.nextask/source.git
nextask enqueue "python train.py" --snapshot --remote ~/.nextask/source.git
```

```toml
[source]
remote = "~/.nextask/source.git"
```

### Gitea

```bash
nextask enqueue "python train.py" --snapshot \
  --remote "http://<user>:<token>@<host>:3000/<user>/snapshots.git"  # HTTP

nextask enqueue "python train.py" --snapshot \
  --remote "git@<host>:<user>/snapshots.git"                         # SSH

git remote add gitea "http://<user>:<token>@<host>:3000/<user>/snapshots.git"
nextask enqueue "python train.py" --snapshot --remote gitea          # git remote name
```

```toml
[source]
remote = "http://<user>:<token>@<host>:3000/<user>/snapshots.git"
```

Set a default remote in `~/.config/nextask/global.toml` to avoid repeating `--remote`.

## Hyperparameter Sweep

```bash
for lr in 0.1 0.01 0.001; do
  nextask enqueue "python train.py --lr $lr" --snapshot --tag sweep=exp3,lr=$lr
done
nextask list --tag sweep=exp3                                        # compare results
```

## GPU Routing

```bash
nextask enqueue "python train.py" --snapshot --tag gpu=a100          # require A100
nextask worker --filter gpu=a100                                     # on the A100 machine
```

## Overnight Batch

```bash
nextask enqueue "python eval.py --dataset test" --snapshot
nextask enqueue "python eval.py --dataset val" --snapshot
nextask worker --daemon                                              # runs in background
nextask list                                                         # check next morning
```
