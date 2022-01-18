<!-- start title -->

# GitHub Action:Atlantis Automation

<!-- end title -->

<!-- start description -->

Checks for result of atlantis plan, approves PR, and runs atlantis apply

<!-- end description -->

<!-- start usage -->

```yaml
- uses: blinkhealth/atlantis-gh-action@main
  with:
    # PR number
    pr_number: ""

    # Token to use for reviewing and posting comments to the PR
    github_token: ""
```

<!-- end usage -->

<!-- start inputs -->

| **Input**          | **Description**                                           | **Default** | **Required** |
| :----------------- | :-------------------------------------------------------- | :---------: | :----------: |
| **`pr_number`**    | PR number                                                 |             |   **true**   |
| **`github_token`** | Token to use for reviewing and posting comments to the PR |             |   **true**   |

<!-- end inputs -->
