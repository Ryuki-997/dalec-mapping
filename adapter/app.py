import os
import sys
import requests
from openai import AzureOpenAI
from dotenv import load_dotenv

load_dotenv()

# Azure OpenAI configuration
azureOpenAiEndpoint = os.getenv("AZURE_OPENAI_ENDPOINT", "")
azureOpenAiKey = os.getenv("AZURE_OPENAI_KEY", "")
azureOpenAiDeployment = os.getenv("AZURE_OPENAI_DEPLOYMENT", "gpt-4")

# Path to skill.md
skillFilePath = os.path.join(os.path.dirname(__file__), "..", "generator", "skills", "dalec-spec-generator", "SKILL.md")

def authorizeGithubRepository(repoUrl: str, githubToken: str = None) -> bool:
  try:
    response = requests.get(repoUrl, timeout=10)
    return response.status_code == 200
  except requests.RequestException:
    return False
    
def loadSkillPrompt() -> str:
  """Load the skill.md file as the system prompt for the agent."""
  try:
    with open(skillFilePath, "r") as f:
      return f.read()
  except FileNotFoundError:
    print(f"Error: skill.md not found at {skillFilePath}")
    return ""

def invokeAgent(repoUrl: str, githubToken: str = None) -> dict:
  """
  Invoke the AI agent with skill.md to generate DALEC spec.
  
  The skill.md defines the agent's behavior and output format.
  """
  skillPrompt = loadSkillPrompt()
  if not skillPrompt:
    return {"status": "error", "message": "Failed to load skill.md"}

  isRepoAccessible = authorizeGithubRepository(repoUrl, githubToken)
  if not isRepoAccessible:
    return {"status": "error", "message": "Repository is not accessible or does not exist."}

  if not azureOpenAiEndpoint or not azureOpenAiKey:
    print("Warning: Azure OpenAI not configured. Running in dry-run mode.")
    return {
      "status": "dry_run",
      "skill_loaded": True,
      "repo_context": repoContext[:500] + "..."
    }
  
  client = AzureOpenAI(
    azure_endpoint=azureOpenAiEndpoint,
    api_key=azureOpenAiKey,
    api_version="2024-02-15-preview"
  )
  
  try:
    response = client.chat.completions.create(
      model=azureOpenAiDeployment,
      messages=[
        {"role": "system", "content": skillPrompt},
        {"role": "user", "content": f"Generate a DALEC spec for this repository:\n\n{repoContext}"}
      ],
      temperature=0.2,
      max_tokens=4000
    )
    
    return {
      "status": "success",
      "dalec_spec": response.choices[0].message.content
    }
  except Exception as e:
    return {"status": "error", "message": str(e)}

def main(args):
  if len(args) < 2:
    print("Usage: python app.py <github_repo_url> [--private]")
    print("\nExamples:")
    print("  python app.py https://github.com/user/repo")
    print("  python app.py https://github.com/user/private-repo --private")
    return 1
  
  repoUrl = args[1]
  isPrivate = "--private" in args
  githubToken = os.getenv("GITHUB_TOKEN") if isPrivate else None
  
  print(f"Invoking DALEC spec generator for: {repoUrl}")
  
  result = invokeAgent(repoUrl, githubToken)
  
  if result.get("status") == "error":
    print(f"Error: {result.get('message')}")
    return 1
  
  print("Generation complete!")
  print(result.get("dalec_spec", result))
  return 0

if __name__ == "__main__":
  sys.exit(main(sys.argv))